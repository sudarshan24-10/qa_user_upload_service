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
		       created_at, updated_at, upload_expires_at, failed_reason
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
		&doc.FailedReason,
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
		       created_at, updated_at, upload_expires_at, failed_reason
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
		&doc.FailedReason,
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

func (r *PostgresRepository) UpdateStatus(ctx context.Context, id string, status model.Status, failedReason *string) error {
	log.Printf("Updating document %s to status %s", id, status)
	query := `
		UPDATE documents
		SET status = $1, updated_at = $2, failed_reason = $3
		WHERE id = $4
	`

	_, err := r.db.Exec(ctx, query, status, time.Now(), failedReason, id)
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
