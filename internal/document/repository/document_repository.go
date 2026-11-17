package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/model"
)

type DocumentRepository interface {
	Create(ctx context.Context, doc *model.Document) (*model.Document, error)

	GetByID(ctx context.Context, id string) (*model.Document, error)

	UpdateStatus(ctx context.Context, id string, status model.Status, failureStage *model.FailureStage, failureReason *string) error

	BeginTx(ctx context.Context) (pgx.Tx, error)

	UpdateStatusWithTx(ctx context.Context, tx pgx.Tx, id string, status model.Status) error

	GetPendingUploads(ctx context.Context, limit int) ([]*model.Document, error)

	GetExpiredUploads(ctx context.Context, limit int) ([]*model.Document, error)

	GetTimedOutProcessing(ctx context.Context, limit int, timeout time.Duration) ([]*model.Document, error)

	// ==================== Explicit State Transition Methods ====================

	// TransitionInitiatedToUploaded safely transitions from INITIATED to UPLOADED.
	// Used by CompleteUpload (primary path).
	// Returns error if state is invalid or document not found.
	TransitionInitiatedToUploaded(ctx context.Context, tx pgx.Tx, documentID string) error

	// EnsureUploadedStatus ensures document is in UPLOADED state.
	// Allows transition from INITIATED or idempotent if already UPLOADED.
	// Used by FinalizeUpload (recovery path).
	// Returns (changed bool, error) where changed=true if state was actually updated.
	EnsureUploadedStatus(ctx context.Context, tx pgx.Tx, documentID string) (bool, error)

	// TransitionToProcessing transitions from UPLOADED to PROCESSING.
	// Used by background workers.
	TransitionToProcessing(ctx context.Context, tx pgx.Tx, documentID string) error

	// TransitionToCompleted transitions from PROCESSING to COMPLETED.
	// Used by background workers.
	TransitionToCompleted(ctx context.Context, tx pgx.Tx, documentID string) error

	// TransitionAnyToFailed transitions from INITIATED, UPLOADED or PROCESSING to FAILED.
	// Used for failure scenarios.
	TransitionAnyToFailed(ctx context.Context, tx pgx.Tx, documentID string, failureStage model.FailureStage, failureReason string) error

	// TransitionExpiredUploadToFailed transitions INITIATED to FAILED only when
	// upload_expires_at has passed. Used by the upload expiry worker.
	TransitionExpiredUploadToFailed(ctx context.Context, tx pgx.Tx, documentID string, failureStage model.FailureStage, failureReason string) error

	// TransitionInitiatedToFailed transitions INITIATED to FAILED (guarded, no-op otherwise).
	// Used by the missing-file paths of CompleteUpload and FinalizeUpload.
	TransitionInitiatedToFailed(ctx context.Context, documentID string, failureStage model.FailureStage, failureReason string) (bool, error)
}
