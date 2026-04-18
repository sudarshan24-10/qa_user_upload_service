package app

import (
	"log"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/config"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/infrastructure/database"

	userHandler "github.com/sudarshan24-10/qa_user_upload_service/internal/user/handler"
	userRepo "github.com/sudarshan24-10/qa_user_upload_service/internal/user/repository"
	userService "github.com/sudarshan24-10/qa_user_upload_service/internal/user/service"

	docHandler "github.com/sudarshan24-10/qa_user_upload_service/internal/document/handler"
	docRepo "github.com/sudarshan24-10/qa_user_upload_service/internal/document/repository"
	docService "github.com/sudarshan24-10/qa_user_upload_service/internal/document/service"
	docStorage "github.com/sudarshan24-10/qa_user_upload_service/internal/document/storage"
	outboxRepo "github.com/sudarshan24-10/qa_user_upload_service/internal/outbox/repository"
)

type App struct {
	router *gin.Engine
	cfg    *config.Config
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

	var userService userService.UserService = userService.NewUserService(userRepository)

	var userHandler *userHandler.UserHandler = userHandler.NewUserHandler(userService)

	// ---------------- DOCUMENT MODULE ----------------

	var documentRepository docRepo.DocumentRepository = docRepo.NewPostgresRepository(dbFactory.PostgresDB)
	var outboxRepo outboxRepo.OutboxRepository = outboxRepo.NewPostgresOutboxRepository(dbFactory.PostgresDB)
	var documentStorage docStorage.ObjectStorage = docStorage.NewMiniIOStorage(minioClient, cfg.BucketName)
	var documentService docService.DocumentService = docService.NewDocumentService(documentRepository, outboxRepo, validate, documentStorage)

	var documentHandler *docHandler.UploadHandler = docHandler.NewUploadHandler(documentService)

	router := gin.Default()

	// Routes
	userRoutes := router.Group("/users")
	{
		userRoutes.POST("/register", userHandler.Register)
		userRoutes.POST("/login", userHandler.Login)
		userRoutes.POST("/verify", userHandler.VerifyOTP)
	}

	documentRoutes := router.Group("/documents")
	{
		documentRoutes.POST("/initiate", documentHandler.InitiateUpload)
		documentRoutes.POST("/complete", documentHandler.CompleteUpload)
		documentRoutes.POST("/fail", documentHandler.FailUpload)
		documentRoutes.POST("/finalize", documentHandler.FinalizeUpload)
		documentRoutes.GET("/:id", documentHandler.GetDocument)
	}

	return &App{
		router: router,
		cfg:    cfg,
	}
}

func (a *App) Run() {
	if err := a.router.Run(":8080"); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}
