package upload_expiry

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/model"
)

const (
	defaultUploadExpiryBatchSize = 100
	defaultUploadExpiryInterval  = 30 * time.Second
	uploadExpiryFailureStage     = model.FailureStageUpload
	uploadExpiryFailureReason    = "upload expired"
)

type uploadExpiryStore interface {
	GetExpiredUploads(ctx context.Context, limit int) ([]*model.Document, error)
	BeginTx(ctx context.Context) (pgx.Tx, error)
	TransitionExpiredUploadToFailed(ctx context.Context, tx pgx.Tx, documentID string, failureStage model.FailureStage, failureReason string) error
}

type UploadExpiryWorker struct {
	store     uploadExpiryStore
	batchSize int
	interval  time.Duration
}

func NewUploadExpiryWorker(store uploadExpiryStore, pollInterval time.Duration) *UploadExpiryWorker {
	interval := pollInterval
	if interval <= 0 {
		interval = defaultUploadExpiryInterval
	}

	return &UploadExpiryWorker{
		store:     store,
		batchSize: defaultUploadExpiryBatchSize,
		interval:  interval,
	}
}

func (w *UploadExpiryWorker) Run(ctx context.Context) {
	w.processOnce(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.processOnce(ctx)
		}
	}
}

func (w *UploadExpiryWorker) processOnce(ctx context.Context) {
	docs, err := w.store.GetExpiredUploads(ctx, w.batchSize)
	if err != nil {
		log.Printf("upload expiry worker: get expired uploads: %v", err)
		return
	}

	for _, doc := range docs {
		if err := w.failExpiredUpload(ctx, doc); err != nil {
			log.Printf("upload expiry worker: failed document %s: %v", doc.ID, err)
		}
	}
}

func (w *UploadExpiryWorker) failExpiredUpload(ctx context.Context, doc *model.Document) error {
	if doc == nil {
		return nil
	}

	tx, err := w.store.BeginTx(ctx)
	if err != nil {
		return err
	}
	if tx != nil {
		defer func() {
			_ = tx.Rollback(ctx)
		}()
	}

	if err := w.store.TransitionExpiredUploadToFailed(ctx, tx, doc.ID, uploadExpiryFailureStage, uploadExpiryFailureReason); err != nil {
		return err
	}

	if tx != nil {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}

	return nil
}
