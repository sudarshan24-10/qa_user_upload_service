package upload_expiry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/model"
)

type fakeUploadExpiryStore struct {
	docs          []*model.Document
	err           error
	failedIDs     []string
	failureStage  model.FailureStage
	failureReason string
	failErrByID   map[string]error
}

func (f *fakeUploadExpiryStore) GetExpiredUploads(ctx context.Context, limit int) ([]*model.Document, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.docs, nil
}

func (f *fakeUploadExpiryStore) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return nil, nil
}

func (f *fakeUploadExpiryStore) TransitionExpiredUploadToFailed(ctx context.Context, tx pgx.Tx, documentID string, failureStage model.FailureStage, failureReason string) error {
	if e, ok := f.failErrByID[documentID]; ok {
		return e
	}
	f.failedIDs = append(f.failedIDs, documentID)
	f.failureStage = failureStage
	f.failureReason = failureReason
	return nil
}

func TestUploadExpiryWorker_ProcessesExpiredInitiatedDocuments(t *testing.T) {
	store := &fakeUploadExpiryStore{
		docs: []*model.Document{{ID: "doc-1", Status: model.StatusInitiated}},
	}

	worker := NewUploadExpiryWorker(store, 30*time.Second)
	worker.processOnce(context.Background())

	if len(store.failedIDs) != 1 {
		t.Fatalf("expected 1 failed document, got %d", len(store.failedIDs))
	}

	if store.failedIDs[0] != "doc-1" {
		t.Fatalf("expected doc-1 to be failed, got %s", store.failedIDs[0])
	}

	if store.failureStage != model.FailureStageUpload {
		t.Fatalf("expected failure stage %s, got %s", model.FailureStageUpload, store.failureStage)
	}

	if store.failureReason != uploadExpiryFailureReason {
		t.Fatalf("expected failure reason %q, got %q", uploadExpiryFailureReason, store.failureReason)
	}
}

func TestUploadExpiryWorker_RaceLostDoesNotFailOtherDocuments(t *testing.T) {
	store := &fakeUploadExpiryStore{
		docs: []*model.Document{
			{ID: "doc-1", Status: model.StatusInitiated},
			{ID: "doc-2", Status: model.StatusInitiated},
		},
		failErrByID: map[string]error{
			"doc-1": &model.InvalidStateTransitionError{
				DocumentID:      "doc-1",
				CurrentStatus:   model.StatusUploaded,
				AttemptedStatus: model.StatusFailed,
			},
		},
	}

	worker := NewUploadExpiryWorker(store, 30*time.Second)
	worker.processOnce(context.Background())

	if len(store.failedIDs) != 1 || store.failedIDs[0] != "doc-2" {
		t.Fatalf("expected only doc-2 to be failed, got %v", store.failedIDs)
	}
}

func TestUploadExpiryWorker_FetchErrorDoesNotTransition(t *testing.T) {
	store := &fakeUploadExpiryStore{err: errors.New("db down")}

	worker := NewUploadExpiryWorker(store, 30*time.Second)
	worker.processOnce(context.Background())

	if len(store.failedIDs) != 0 {
		t.Fatalf("expected no transitions, got %v", store.failedIDs)
	}
}
