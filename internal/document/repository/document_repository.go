package repository

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/model"
)

type DocumentRepository interface {
	Create(ctx context.Context, doc *model.Document) (*model.Document, error)

	GetByID(ctx context.Context, id string) (*model.Document, error)

	UpdateStatus(ctx context.Context, id string, status model.Status, failedReason *string) error

	BeginTx(ctx context.Context) (pgx.Tx, error)

	UpdateStatusWithTx(ctx context.Context, tx pgx.Tx, id string, status model.Status) error

	GetPendingUploads(ctx context.Context, limit int) ([]*model.Document, error)

	GetExpiredUploads(ctx context.Context, limit int) ([]*model.Document, error)
}
