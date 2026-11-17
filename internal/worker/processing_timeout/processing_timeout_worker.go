package processing_timeout

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/model"
)

const (
	defaultProcessingTimeoutBatchSize = 100
	defaultProcessingTimeoutInterval  = 30 * time.Second
	processingTimeoutFailureStage     = model.FailureStageProcessing
	processingTimeoutFailureReason    = "processing timeout"
	processingTimeoutDuration         = 5 * time.Minute
)

type processingTimeoutStore interface {
	GetTimedOutProcessing(ctx context.Context, limit int, timeout time.Duration) ([]*model.Document, error)
	BeginTx(ctx context.Context) (pgx.Tx, error)
	TransitionAnyToFailed(ctx context.Context, tx pgx.Tx, documentID string, failureStage model.FailureStage, failureReason string) error
}

type ProcessingTimeoutWorker struct {
	store     processingTimeoutStore
	batchSize int
	interval  time.Duration
	timeout   time.Duration
}

func NewProcessingTimeoutWorker(store processingTimeoutStore, timeout time.Duration, pollInterval time.Duration) *ProcessingTimeoutWorker {
	if timeout <= 0 {
		timeout = processingTimeoutDuration
	}
	if pollInterval <= 0 {
		pollInterval = defaultProcessingTimeoutInterval
	}

	return &ProcessingTimeoutWorker{
		store:     store,
		batchSize: defaultProcessingTimeoutBatchSize,
		interval:  pollInterval,
		timeout:   timeout,
	}
}

func (w *ProcessingTimeoutWorker) Run(ctx context.Context) {
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

func (w *ProcessingTimeoutWorker) processOnce(ctx context.Context) {
	docs, err := w.store.GetTimedOutProcessing(ctx, w.batchSize, w.timeout)
	if err != nil {
		log.Printf("processing timeout worker: get timed out processing: %v", err)
		return
	}

	for _, doc := range docs {
		if err := w.failTimedOutProcessing(ctx, doc); err != nil {
			log.Printf("processing timeout worker: failed document %s: %v", doc.ID, err)
		}
	}
}

func (w *ProcessingTimeoutWorker) failTimedOutProcessing(ctx context.Context, doc *model.Document) error {
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

	if err := w.store.TransitionAnyToFailed(ctx, tx, doc.ID, processingTimeoutFailureStage, processingTimeoutFailureReason); err != nil {
		return err
	}

	if tx != nil {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}

	return nil
}
