package repository

import (
	"context"
	"sort"
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

	_, err := r.db.Exec(ctx, query,
		event.ID,
		event.AggregateID,
		event.EventType,
		event.Payload,
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

	_, err := tx.Exec(ctx, query,
		event.ID,
		event.AggregateID,
		event.EventType,
		event.Payload,
		event.Status,
		event.RetryCount,
		event.CreatedAt,
	)

	return err
}

func (r *PostgresOutboxRepository) FetchPending(ctx context.Context, limit int) ([]*model.OutboxEvent, error) {
	query := `
		SELECT id, aggregate_id, event_type, payload, status, retry_count, created_at
		FROM outbox_events
		WHERE status = 'PENDING'
		  AND (next_retry_at IS NULL OR next_retry_at <= NOW())
		ORDER BY created_at ASC
		LIMIT $1
	`

	rows, err := r.db.Query(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*model.OutboxEvent
	for rows.Next() {
		e := &model.OutboxEvent{}
		err := rows.Scan(
			&e.ID,
			&e.AggregateID,
			&e.EventType,
			&e.Payload,
			&e.Status,
			&e.RetryCount,
			&e.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return events, nil
}

// ClaimPending claims due PENDING events with FOR UPDATE SKIP LOCKED and leases them so that
// concurrent dispatcher instances never receive the same row. If the dispatcher crashes after
// claiming, the lease expires and the event becomes due again.
func (r *PostgresOutboxRepository) ClaimPending(ctx context.Context, limit int, lease time.Duration) ([]*model.OutboxEvent, error) {
	query := `
		UPDATE outbox_events
		SET next_retry_at = NOW() + ($2 * interval '1 second')
		WHERE id IN (
			SELECT id
			FROM outbox_events
			WHERE status = 'PENDING'
			  AND (next_retry_at IS NULL OR next_retry_at <= NOW())
			ORDER BY created_at ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, aggregate_id, event_type, payload, status, retry_count, created_at
	`

	rows, err := r.db.Query(ctx, query, limit, int(lease.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*model.OutboxEvent
	for rows.Next() {
		e := &model.OutboxEvent{}
		if err := rows.Scan(&e.ID, &e.AggregateID, &e.EventType, &e.Payload, &e.Status, &e.RetryCount, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// RETURNING order is not guaranteed; publish oldest first.
	sort.Slice(events, func(i, j int) bool { return events[i].CreatedAt.Before(events[j].CreatedAt) })
	return events, nil
}

func (r *PostgresOutboxRepository) MarkProcessed(ctx context.Context, id string) error {
	query := `
		UPDATE outbox_events
		SET status = 'PROCESSED', processed_at = NOW()
		WHERE id = $1
	`

	_, err := r.db.Exec(ctx, query, id)
	return err
}

func (r *PostgresOutboxRepository) MarkRetry(ctx context.Context, id string, nextRetryAt time.Time) error {
	query := `
		UPDATE outbox_events
		SET retry_count = retry_count + 1, next_retry_at = $2
		WHERE id = $1 AND status = 'PENDING'
	`

	_, err := r.db.Exec(ctx, query, id, nextRetryAt)
	return err
}

func (r *PostgresOutboxRepository) MarkFailed(ctx context.Context, id string) error {
	query := `
		UPDATE outbox_events
		SET status = 'FAILED', retry_count = retry_count + 1, next_retry_at = NULL
		WHERE id = $1 AND status = 'PENDING'
	`

	_, err := r.db.Exec(ctx, query, id)
	return err
}
