package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/outbox/model"
)

type OutboxRepository interface {
	Create(ctx context.Context, event *model.OutboxEvent) error
	CreateWithTx(ctx context.Context, tx pgx.Tx, event *model.OutboxEvent) error
	FetchPending(ctx context.Context, limit int) ([]*model.OutboxEvent, error)
	// ClaimPending atomically claims up to limit due PENDING events for this dispatcher by
	// leasing them (next_retry_at = NOW() + lease). Safe with several dispatcher instances.
	ClaimPending(ctx context.Context, limit int, lease time.Duration) ([]*model.OutboxEvent, error)
	MarkProcessed(ctx context.Context, id string) error
	MarkRetry(ctx context.Context, id string, nextRetryAt time.Time) error
	// MarkFailed permanently stops retrying a PENDING event.
	MarkFailed(ctx context.Context, id string) error
}
