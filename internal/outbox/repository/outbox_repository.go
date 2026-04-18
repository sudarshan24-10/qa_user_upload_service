package repository

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/outbox/model"
)

type OutboxRepository interface {
	Create(ctx context.Context, event *model.OutboxEvent) error
	CreateWithTx(ctx context.Context, tx pgx.Tx, event *model.OutboxEvent) error
}
