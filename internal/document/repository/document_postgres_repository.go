package repository

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/model"
)

type PostgresRepository struct {
	db *pgxpool.Pool
}

func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) Create(ctx context.Context, doc *model.Document) (*model.Document, error) {
	query := `
		INSERT INTO documents (
			id,
			user_id,
			object_key,
			file_name,
			size,
			status,
			idempotency_key,
			content_type,
			metadata,
			created_at,
			updated_at,
			upload_expires_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id, user_id, object_key, file_name, size,
		          status, idempotency_key, content_type, metadata,
		          created_at, updated_at, upload_expires_at
	`

	metadataJSON, err := json.Marshal(doc.Metadata)
	if err != nil {
		return nil, err
	}

	var createdDoc model.Document
	var returnedMetadata []byte

	err = r.db.QueryRow(ctx, query,
		doc.ID,
		doc.UserID,
		doc.ObjectKey,
		doc.FileName,
		doc.Size,
		doc.Status,
		doc.IdempotencyKey,
		doc.ContentType,
		metadataJSON,
		doc.CreatedAt,
		doc.UpdatedAt,
		doc.UploadExpiresAt,
	).Scan(
		&createdDoc.ID,
		&createdDoc.UserID,
		&createdDoc.ObjectKey,
		&createdDoc.FileName,
		&createdDoc.Size,
		&createdDoc.Status,
		&createdDoc.IdempotencyKey,
		&createdDoc.ContentType,
		&returnedMetadata,
		&createdDoc.CreatedAt,
		&createdDoc.UpdatedAt,
		&createdDoc.UploadExpiresAt,
	)

	if err == nil {
		if len(returnedMetadata) > 0 {
			if err := json.Unmarshal(returnedMetadata, &createdDoc.Metadata); err != nil {
				return nil, err
			}
		} else {
			createdDoc.Metadata = map[string]string{}
		}
		return &createdDoc, nil
	}

	if err == pgx.ErrNoRows {
		existingDoc, err := r.getByIdempotencyKey(ctx, doc.IdempotencyKey)
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			return r.getByIdempotencyKey(ctx, doc.IdempotencyKey)
		}
		return existingDoc, nil
	}

	return nil, err
}

func (r *PostgresRepository) getByIdempotencyKey(ctx context.Context, key string) (*model.Document, error) {
	query := `
		SELECT id, user_id, object_key, file_name, size,
		       status, idempotency_key, content_type, metadata,
		       created_at, updated_at, upload_expires_at,
		       failure_stage, failure_reason, failed_at
		FROM documents
		WHERE idempotency_key = $1
	`

	row := r.db.QueryRow(ctx, query, key)

	var doc model.Document
	var metadataJSON []byte

	err := row.Scan(
		&doc.ID,
		&doc.UserID,
		&doc.ObjectKey,
		&doc.FileName,
		&doc.Size,
		&doc.Status,
		&doc.IdempotencyKey,
		&doc.ContentType,
		&metadataJSON,
		&doc.CreatedAt,
		&doc.UpdatedAt,
		&doc.UploadExpiresAt,
		&doc.FailureStage,
		&doc.FailureReason,
		&doc.FailedAt,
	)

	if err != nil {
		return nil, err
	}

	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &doc.Metadata); err != nil {
			return nil, err
		}
	} else {
		doc.Metadata = map[string]string{}
	}

	return &doc, nil
}

func (r *PostgresRepository) GetByID(ctx context.Context, id string) (*model.Document, error) {
	query := `
		SELECT id, user_id, object_key, file_name, size,
		       status, idempotency_key, content_type, metadata,
		       created_at, updated_at, upload_expires_at,
		       failure_stage, failure_reason, failed_at
		FROM documents
		WHERE id = $1
	`

	row := r.db.QueryRow(ctx, query, id)

	var doc model.Document
	var metadataJSON []byte

	err := row.Scan(
		&doc.ID,
		&doc.UserID,
		&doc.ObjectKey,
		&doc.FileName,
		&doc.Size,
		&doc.Status,
		&doc.IdempotencyKey,
		&doc.ContentType,
		&metadataJSON,
		&doc.CreatedAt,
		&doc.UpdatedAt,
		&doc.UploadExpiresAt,
		&doc.FailureStage,
		&doc.FailureReason,
		&doc.FailedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &doc.Metadata); err != nil {
			return nil, err
		}
	} else {
		doc.Metadata = map[string]string{}
	}

	return &doc, nil
}

func (r *PostgresRepository) UpdateStatus(ctx context.Context, id string, status model.Status, failureStage *model.FailureStage, failureReason *string) error {
	log.Printf("Updating document %s to status %s", id, status)

	var failedAt *time.Time
	if status == model.StatusFailed {
		now := time.Now()
		failedAt = &now
	}

	query := `
		UPDATE documents
		SET status = $1, updated_at = $2,
		    failure_stage = $3, failure_reason = $4, failed_at = $5
		WHERE id = $6
	`

	_, err := r.db.Exec(ctx, query, status, time.Now(), failureStage, failureReason, failedAt, id)
	return err
}

func (r *PostgresRepository) GetPendingUploads(ctx context.Context, limit int) ([]*model.Document, error) {
	query := `
		SELECT id, user_id, object_key, file_name, size,
		       status, idempotency_key, content_type, metadata,
		       created_at, updated_at, upload_expires_at
		FROM documents
		WHERE status = 'INITIATED'
		AND created_at < NOW() - INTERVAL '2 minutes'
		AND upload_expires_at > NOW()
		LIMIT $1
	`

	rows, err := r.db.Query(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []*model.Document

	for rows.Next() {
		var doc model.Document
		var metadataJSON []byte

		err := rows.Scan(
			&doc.ID,
			&doc.UserID,
			&doc.ObjectKey,
			&doc.FileName,
			&doc.Size,
			&doc.Status,
			&doc.IdempotencyKey,
			&doc.ContentType,
			&metadataJSON,
			&doc.CreatedAt,
			&doc.UpdatedAt,
			&doc.UploadExpiresAt,
		)
		if err != nil {
			return nil, err
		}

		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &doc.Metadata); err != nil {
				return nil, err
			}
		} else {
			doc.Metadata = map[string]string{}
		}

		docs = append(docs, &doc)
	}

	return docs, nil
}

func (r *PostgresRepository) GetExpiredUploads(ctx context.Context, limit int) ([]*model.Document, error) {
	query := `
		SELECT id, user_id, object_key, file_name, size,
		       status, idempotency_key, content_type, metadata,
		       created_at, updated_at, upload_expires_at
		FROM documents
		WHERE status = 'INITIATED'
		AND upload_expires_at <= NOW()
		LIMIT $1
	`

	rows, err := r.db.Query(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []*model.Document

	for rows.Next() {
		var doc model.Document
		var metadataJSON []byte

		err := rows.Scan(
			&doc.ID,
			&doc.UserID,
			&doc.ObjectKey,
			&doc.FileName,
			&doc.Size,
			&doc.Status,
			&doc.IdempotencyKey,
			&doc.ContentType,
			&metadataJSON,
			&doc.CreatedAt,
			&doc.UpdatedAt,
			&doc.UploadExpiresAt,
		)
		if err != nil {
			return nil, err
		}

		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &doc.Metadata); err != nil {
				return nil, err
			}
		} else {
			doc.Metadata = map[string]string{}
		}

		docs = append(docs, &doc)
	}

	return docs, nil
}

func (r *PostgresRepository) GetTimedOutProcessing(ctx context.Context, limit int, timeout time.Duration) ([]*model.Document, error) {
	query := `
		SELECT id, user_id, object_key, file_name, size,
		       status, idempotency_key, content_type, metadata,
		       created_at, updated_at, upload_expires_at
		FROM documents
		WHERE status = 'PROCESSING'
		AND updated_at <= NOW() - ($2)::interval
		LIMIT $1
	`

	rows, err := r.db.Query(ctx, query, limit, timeout.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []*model.Document

	for rows.Next() {
		var doc model.Document
		var metadataJSON []byte

		err := rows.Scan(
			&doc.ID,
			&doc.UserID,
			&doc.ObjectKey,
			&doc.FileName,
			&doc.Size,
			&doc.Status,
			&doc.IdempotencyKey,
			&doc.ContentType,
			&metadataJSON,
			&doc.CreatedAt,
			&doc.UpdatedAt,
			&doc.UploadExpiresAt,
		)
		if err != nil {
			return nil, err
		}

		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &doc.Metadata); err != nil {
				return nil, err
			}
		} else {
			doc.Metadata = map[string]string{}
		}

		docs = append(docs, &doc)
	}

	return docs, nil
}

func (r *PostgresRepository) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return r.db.Begin(ctx)
}

func (r *PostgresRepository) UpdateStatusWithTx(
	ctx context.Context,
	tx pgx.Tx,
	id string,
	status model.Status,
) error {

	query := `
		UPDATE documents
		SET status = $1, updated_at = $2
		WHERE id = $3
	`

	_, err := tx.Exec(ctx, query, status, time.Now(), id)
	return err
}

// ==================== Explicit State Transition Methods ====================

// TransitionInitiatedToUploaded safely transitions document from INITIATED to UPLOADED state.
// Returns error if:
// - Document not found
// - Current state is not INITIATED
// This is used by CompleteUpload (primary path).
func (r *PostgresRepository) TransitionInitiatedToUploaded(ctx context.Context, tx pgx.Tx, documentID string) error {
	query := `
		UPDATE documents
		SET status = $1, updated_at = $2
		WHERE id = $3 AND status = $4
		RETURNING id
	`

	var returnedID string
	err := tx.QueryRow(ctx, query, model.StatusUploaded, time.Now(), documentID, model.StatusInitiated).Scan(&returnedID)

	if err == pgx.ErrNoRows {
		// Document either doesn't exist or isn't in INITIATED state
		// Fetch to determine which error
		doc, fetchErr := r.GetByID(ctx, documentID)
		if fetchErr != nil {
			return fetchErr
		}
		if doc == nil {
			return &model.DocumentNotFoundError{DocumentID: documentID}
		}
		return &model.InvalidStateTransitionError{
			DocumentID:      documentID,
			CurrentStatus:   doc.Status,
			AttemptedStatus: model.StatusUploaded,
		}
	}

	return err
}

// EnsureUploadedStatus safely ensures document is in UPLOADED state.
// Allows transition from either INITIATED or UPLOADED (idempotent for recovery).
// Returns error only if document is in invalid state (PROCESSING, COMPLETED, FAILED).
// Used by FinalizeUpload (recovery path).
// Returns (changed bool, error):
//   - changed=true: state was changed from INITIATED to UPLOADED
//   - changed=false: state was already UPLOADED
//   - error: state is invalid or document not found
func (r *PostgresRepository) EnsureUploadedStatus(ctx context.Context, tx pgx.Tx, documentID string) (bool, error) {
	query := `
		UPDATE documents
		SET status = $1, updated_at = $2
		WHERE id = $3 AND status = $4
		RETURNING id
	`

	var returnedID string
	err := tx.QueryRow(ctx, query, model.StatusUploaded, time.Now(), documentID, model.StatusInitiated).Scan(&returnedID)

	if err == nil {
		// Successfully transitioned from INITIATED to UPLOADED
		return true, nil
	}

	if err != pgx.ErrNoRows {
		return false, err
	}

	// No rows affected - check current state
	doc, fetchErr := r.GetByID(ctx, documentID)
	if fetchErr != nil {
		return false, fetchErr
	}
	if doc == nil {
		return false, &model.DocumentNotFoundError{DocumentID: documentID}
	}

	// Already uploaded - idempotent, not an error
	if doc.Status == model.StatusUploaded {
		return false, nil
	}

	// Invalid state for recovery
	return false, &model.InvalidStateTransitionError{
		DocumentID:      documentID,
		CurrentStatus:   doc.Status,
		AttemptedStatus: model.StatusUploaded,
	}
}

// TransitionToProcessing safely transitions document from UPLOADED to PROCESSING state.
// Used by background workers to mark document as being processed.
// Returns error if current state is not UPLOADED.
func (r *PostgresRepository) TransitionToProcessing(ctx context.Context, tx pgx.Tx, documentID string) error {
	query := `
		UPDATE documents
		SET status = $1, updated_at = $2
		WHERE id = $3 AND status = $4
		RETURNING id
	`

	var returnedID string
	err := tx.QueryRow(ctx, query, model.StatusProcessing, time.Now(), documentID, model.StatusUploaded).Scan(&returnedID)

	if err == pgx.ErrNoRows {
		doc, fetchErr := r.GetByID(ctx, documentID)
		if fetchErr != nil {
			return fetchErr
		}
		if doc == nil {
			return &model.DocumentNotFoundError{DocumentID: documentID}
		}
		return &model.InvalidStateTransitionError{
			DocumentID:      documentID,
			CurrentStatus:   doc.Status,
			AttemptedStatus: model.StatusProcessing,
		}
	}

	return err
}

// TransitionToCompleted safely transitions document from PROCESSING to COMPLETED state.
// Used by background workers after successful processing.
// Returns error if current state is not PROCESSING.
func (r *PostgresRepository) TransitionToCompleted(ctx context.Context, tx pgx.Tx, documentID string) error {
	query := `
		UPDATE documents
		SET status = $1, updated_at = $2
		WHERE id = $3 AND status = $4
		RETURNING id
	`

	var returnedID string
	err := tx.QueryRow(ctx, query, model.StatusCompleted, time.Now(), documentID, model.StatusProcessing).Scan(&returnedID)

	if err == pgx.ErrNoRows {
		doc, fetchErr := r.GetByID(ctx, documentID)
		if fetchErr != nil {
			return fetchErr
		}
		if doc == nil {
			return &model.DocumentNotFoundError{DocumentID: documentID}
		}
		return &model.InvalidStateTransitionError{
			DocumentID:      documentID,
			CurrentStatus:   doc.Status,
			AttemptedStatus: model.StatusCompleted,
		}
	}

	return err
}

// TransitionAnyToFailed safely transitions document from INITIATED, UPLOADED, or PROCESSING to FAILED.
// Rejects COMPLETED and FAILED documents.
// Sets the failure reason for audit trail.
// Returns error if state is already COMPLETED or document not found.
func (r *PostgresRepository) TransitionAnyToFailed(ctx context.Context, tx pgx.Tx, documentID string, failureStage model.FailureStage, failureReason string) error {
	now := time.Now()
	query := `
		UPDATE documents
		SET status = $1, failure_stage = $2, failure_reason = $3,
		    failed_at = $4, updated_at = $4
		WHERE id = $5
		  AND status IN ('INITIATED', 'UPLOADED', 'PROCESSING')
		RETURNING id
	`

	var returnedID string
	err := tx.QueryRow(ctx, query, model.StatusFailed, failureStage, failureReason, now, documentID).Scan(&returnedID)

	if err == pgx.ErrNoRows {
		doc, fetchErr := r.GetByID(ctx, documentID)
		if fetchErr != nil {
			return fetchErr
		}
		if doc == nil {
			return &model.DocumentNotFoundError{DocumentID: documentID}
		}
		// Document already completed - can't fail
		return &model.InvalidStateTransitionError{
			DocumentID:      documentID,
			CurrentStatus:   doc.Status,
			AttemptedStatus: model.StatusFailed,
		}
	}

	return err
}

// TransitionExpiredUploadToFailed transitions a document from INITIATED to FAILED
// only if its upload window has actually expired.
// Used by UploadExpiryWorker so a stale candidate list can never fail a document
// that was completed (UPLOADED) or otherwise moved on in the meantime.
func (r *PostgresRepository) TransitionExpiredUploadToFailed(ctx context.Context, tx pgx.Tx, documentID string, failureStage model.FailureStage, failureReason string) error {
	now := time.Now()
	query := `
		UPDATE documents
		SET status = $1, failure_stage = $2, failure_reason = $3,
		    failed_at = $4, updated_at = $4
		WHERE id = $5
		  AND status = 'INITIATED'
		  AND upload_expires_at <= NOW()
		RETURNING id
	`

	var returnedID string
	err := tx.QueryRow(ctx, query, model.StatusFailed, failureStage, failureReason, now, documentID).Scan(&returnedID)

	if err == pgx.ErrNoRows {
		doc, fetchErr := r.GetByID(ctx, documentID)
		if fetchErr != nil {
			return fetchErr
		}
		if doc == nil {
			return &model.DocumentNotFoundError{DocumentID: documentID}
		}
		return &model.InvalidStateTransitionError{
			DocumentID:      documentID,
			CurrentStatus:   doc.Status,
			AttemptedStatus: model.StatusFailed,
		}
	}

	return err
}

// TransitionInitiatedToFailed transitions a document from INITIATED to FAILED.
// Used when storage verification finds no object during CompleteUpload/FinalizeUpload.
// Returns (changed bool, error):
//   - changed=true: document was INITIATED and is now FAILED
//   - changed=false: document was not INITIATED (concurrent completion/failure), nothing was written
func (r *PostgresRepository) TransitionInitiatedToFailed(ctx context.Context, documentID string, failureStage model.FailureStage, failureReason string) (bool, error) {
	now := time.Now()
	query := `
		UPDATE documents
		SET status = $1, failure_stage = $2, failure_reason = $3,
		    failed_at = $4, updated_at = $4
		WHERE id = $5
		  AND status = 'INITIATED'
	`

	tag, err := r.db.Exec(ctx, query, model.StatusFailed, failureStage, failureReason, now, documentID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
