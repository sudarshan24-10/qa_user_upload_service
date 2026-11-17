package app

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/config"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/infrastructure/database"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/middleware"

	userHandler "github.com/sudarshan24-10/qa_user_upload_service/internal/user/handler"
	userRepo "github.com/sudarshan24-10/qa_user_upload_service/internal/user/repository"
	userService "github.com/sudarshan24-10/qa_user_upload_service/internal/user/service"

	docHandler "github.com/sudarshan24-10/qa_user_upload_service/internal/document/handler"
	docRepo "github.com/sudarshan24-10/qa_user_upload_service/internal/document/repository"
	docService "github.com/sudarshan24-10/qa_user_upload_service/internal/document/service"
	docStorage "github.com/sudarshan24-10/qa_user_upload_service/internal/document/storage"
	outboxRepo "github.com/sudarshan24-10/qa_user_upload_service/internal/outbox/repository"
	outboxDispatcher "github.com/sudarshan24-10/qa_user_upload_service/internal/worker/outbox_dispatcher"
	processingTimeoutWorker "github.com/sudarshan24-10/qa_user_upload_service/internal/worker/processing_timeout"
	uploadExpiryWorker "github.com/sudarshan24-10/qa_user_upload_service/internal/worker/upload_expiry"
)

type App struct {
	server            *http.Server
	cfg               *config.Config
	dbFactory         *database.Factory
	dispatcher        *outboxDispatcher.OutboxDispatcher
	uploadExpiry      *uploadExpiryWorker.UploadExpiryWorker
	processingTimeout *processingTimeoutWorker.ProcessingTimeoutWorker
	publisher         outboxDispatcher.Publisher
	rateLimiter       *middleware.RateLimiter
}

func NewApp() *App {

	cfg := config.Load()
	minioClient, err := minio.New(cfg.MinioEndpoint, &minio.Options{
		Creds: credentials.NewStaticV4(
			cfg.ObjectStorage_access_key,
			cfg.ObjectStorage_secret_key,
			"",
		),
		Secure: false,
	})
	if err != nil {
		log.Fatalf("failed to initialize Minio client: %v", err)
	}

	dbFactory, err := database.NewFactory(cfg.PostgresURL, cfg.MongoURL)
	if err != nil {
		log.Fatalf("failed to initialize database: %v", err)
	}

	validate := validator.New()

	// ---------------- USER MODULE ----------------

	var userRepository userRepo.UserRepository = userRepo.NewUserPostgresRepository(dbFactory.PostgresDB)

	// The hasher is injected so tests can swap it for a fast/fake one; production uses
	// real bcrypt at the default cost.
	var userService userService.UserService = userService.NewUserService(userRepository, userService.NewBcryptHasher(userService.DefaultPasswordHashCost))

	var userHandler *userHandler.UserHandler = userHandler.NewUserHandler(userService)

	// ---------------- DOCUMENT MODULE ----------------

	var documentRepository docRepo.DocumentRepository = docRepo.NewPostgresRepository(dbFactory.PostgresDB)
	var outboxRepo outboxRepo.OutboxRepository = outboxRepo.NewPostgresOutboxRepository(dbFactory.PostgresDB)
	var documentStorage docStorage.ObjectStorage = docStorage.NewMiniIOStorage(minioClient, cfg.BucketName)
	var documentService docService.DocumentService = docService.NewDocumentService(documentRepository, outboxRepo, validate, documentStorage, cfg)

	var documentHandler *docHandler.UploadHandler = docHandler.NewUploadHandler(documentService)

	publisher, err := outboxDispatcher.NewRedisOutboxPublisher(cfg)
	if err != nil {
		log.Fatalf("failed to initialize Redis publisher: %v", err)
	}
	dispatcher := outboxDispatcher.NewOutboxDispatcher(outboxRepo, publisher)
	uploadExpiry := uploadExpiryWorker.NewUploadExpiryWorker(documentRepository, cfg.WorkerPollInterval)
	processingTimeout := processingTimeoutWorker.NewProcessingTimeoutWorker(documentRepository, cfg.ProcessingTimeout, cfg.WorkerPollInterval)

	router := gin.Default()

	// Trust no proxy: the rate limiter keys on the real socket IP, not on a spoofable
	// X-Forwarded-For header. Revisit (list the proxy's address) if nginx or a load
	// balancer is ever put in front of this service.
	if err := router.SetTrustedProxies(nil); err != nil {
		log.Fatalf("failed to set trusted proxies: %v", err)
	}

	// Order matters: the limiter runs first so floods are rejected before any
	// bcrypt/handler work, then the 1 MB body cap. Future CORS goes before these and
	// auth middleware goes on the route groups after them.
	rateLimiter := middleware.NewRateLimiter()
	router.Use(rateLimiter.Middleware(), middleware.MaxBody(1<<20))

	// Routes
	userRoutes := router.Group("/users")
	{
		userRoutes.POST("/register", userHandler.Register)
		userRoutes.POST("/login", userHandler.Login)
	}

	documentRoutes := router.Group("/documents")
	{
		documentRoutes.POST("/initiate", documentHandler.InitiateUpload)
		documentRoutes.POST("/complete", documentHandler.CompleteUpload)
		documentRoutes.POST("/fail", documentHandler.MarkDocumentFailed)
		documentRoutes.POST("/finalize", documentHandler.FinalizeUpload)
		documentRoutes.GET("/:id", documentHandler.GetDocument)
	}

	return &App{
		server: &http.Server{
			Addr:    ":8000",
			Handler: router,
			// Timeouts stop slow or idle clients from holding connections open.
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
		rateLimiter:       rateLimiter,
		cfg:               cfg,
		dbFactory:         dbFactory,
		dispatcher:        dispatcher,
		uploadExpiry:      uploadExpiry,
		processingTimeout: processingTimeout,
		publisher:         publisher,
	}
}

func (a *App) Run() {
	defer func() {
		if err := a.dbFactory.Close(context.Background()); err != nil {
			log.Printf("database shutdown failed: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dispatcherDone := make(chan struct{})
	go func() {
		defer close(dispatcherDone)
		a.dispatcher.Run(ctx)
	}()

	uploadExpiryDone := make(chan struct{})
	go func() {
		defer close(uploadExpiryDone)
		a.uploadExpiry.Run(ctx)
	}()

	processingTimeoutDone := make(chan struct{})
	go func() {
		defer close(processingTimeoutDone)
		a.processingTimeout.Run(ctx)
	}()

	rateLimiterDone := make(chan struct{})
	go func() {
		defer close(rateLimiterDone)
		a.rateLimiter.Run(ctx)
	}()

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- a.server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server stopped unexpectedly: %v", err)
		}
		stop()
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := a.server.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP server shutdown failed: %v", err)
		}
	}

	<-dispatcherDone
	<-uploadExpiryDone
	<-processingTimeoutDone
	<-rateLimiterDone
	if err := a.publisher.Close(context.Background()); err != nil {
		log.Printf("publisher shutdown failed: %v", err)
	}
}
