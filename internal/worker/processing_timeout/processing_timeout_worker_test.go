package processing_timeout

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/model"
)

type fakeProcessingTimeoutStore struct {
	docs          []*model.Document
	err           error
	failedIDs     []string
	failureStage  model.FailureStage
	failureReason string
}

func (f *fakeProcessingTimeoutStore) GetTimedOutProcessing(ctx context.Context, limit int, timeout time.Duration) ([]*model.Document, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.docs, nil
}

func (f *fakeProcessingTimeoutStore) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return nil, nil
}

func (f *fakeProcessingTimeoutStore) TransitionAnyToFailed(ctx context.Context, tx pgx.Tx, documentID string, failureStage model.FailureStage, failureReason string) error {
	f.failedIDs = append(f.failedIDs, documentID)
	f.failureStage = failureStage
	f.failureReason = failureReason
	return nil
}

func TestProcessingTimeoutWorker_ProcessesTimedOutProcessingDocuments(t *testing.T) {
	store := &fakeProcessingTimeoutStore{
		docs: []*model.Document{{ID: "doc-2", Status: model.StatusProcessing}},
	}

	worker := NewProcessingTimeoutWorker(store, 5*time.Minute, 30*time.Second)
	worker.processOnce(context.Background())

	if len(store.failedIDs) != 1 {
		t.Fatalf("expected 1 failed document, got %d", len(store.failedIDs))
	}

	if store.failedIDs[0] != "doc-2" {
		t.Fatalf("expected doc-2 to be failed, got %s", store.failedIDs[0])
	}

	if store.failureStage != model.FailureStageProcessing {
		t.Fatalf("expected failure stage %s, got %s", model.FailureStageProcessing, store.failureStage)
	}

	if store.failureReason != processingTimeoutFailureReason {
		t.Fatalf("expected failure reason %q, got %q", processingTimeoutFailureReason, store.failureReason)
	}
}
