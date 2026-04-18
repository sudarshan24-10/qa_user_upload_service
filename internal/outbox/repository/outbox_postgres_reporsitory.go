package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/outbox/model"
)

type PostgresOutboxRepository struct {
	db *pgxpool.Pool
}

func NewPostgresOutboxRepository(db *pgxpool.Pool) *PostgresOutboxRepository {
	return &PostgresOutboxRepository{db: db}
}

func (r *PostgresOutboxRepository) Create(ctx context.Context, event *model.OutboxEvent) error {
	query := `
		INSERT INTO outbox_events (
			id,
			aggregate_id,
			event_type,
			payload,
			status,
			retry_count,
			next_retry_at,
			created_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`

	if event.ID == "" {
		event.ID = uuid.New().String()
	}

	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}

	payloadJSON, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}

	_, err = r.db.Exec(ctx, query,
		event.ID,
		event.AggregateID,
		event.EventType,
		payloadJSON,
		event.Status,
		event.RetryCount,
		event.NextRetryAt,
		event.CreatedAt,
	)

	return err
}

func (r *PostgresOutboxRepository) CreateWithTx(
	ctx context.Context,
	tx pgx.Tx,
	event *model.OutboxEvent,
) error {

	query := `
		INSERT INTO outbox_events (
			id,
			aggregate_id,
			event_type,
			payload,
			status,
			retry_count,
			created_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
	`

	if event.ID == "" {
		event.ID = uuid.New().String()
	}

	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}

	payloadJSON, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, query,
		event.ID,
		event.AggregateID,
		event.EventType,
		payloadJSON,
		event.Status,
		event.RetryCount,
		event.CreatedAt,
	)

	return err
}
