package outbox_dispatcher

import (
	"context"
	"log"
	"time"

	"github.com/sudarshan24-10/qa_user_upload_service/internal/outbox/model"
)

const (
	defaultBatchSize = 100
	defaultInterval  = 5 * time.Second
	baseRetryDelay   = 1 * time.Second
	maxRetryDelay    = 5 * time.Minute

	// maxPublishAttempts must outlast a realistic broker outage: failures are almost always
	// infrastructure, not bad payloads. With the backoff above this is roughly 3.7 hours.
	maxPublishAttempts = 50

	// claimLease is how long a claimed event is reserved for this dispatcher.
	claimLease = 60 * time.Second
)

type outboxStore interface {
	ClaimPending(ctx context.Context, limit int, lease time.Duration) ([]*model.OutboxEvent, error)
	MarkProcessed(ctx context.Context, id string) error
	MarkRetry(ctx context.Context, id string, nextRetryAt time.Time) error
	MarkFailed(ctx context.Context, id string) error
}

type OutboxDispatcher struct {
	store     outboxStore
	publisher Publisher
	batchSize int
	interval  time.Duration
}

func NewOutboxDispatcher(store outboxStore, publisher Publisher) *OutboxDispatcher {
	return &OutboxDispatcher{
		store:     store,
		publisher: publisher,
		batchSize: defaultBatchSize,
		interval:  defaultInterval,
	}
}

func (d *OutboxDispatcher) Run(ctx context.Context) {
	d.processBatch(ctx)

	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.processBatch(ctx)
		}
	}
}

func (d *OutboxDispatcher) processBatch(ctx context.Context) {
	events, err := d.store.ClaimPending(ctx, d.batchSize, claimLease)
	if err != nil {
		log.Printf("outbox dispatcher: claim pending events: %v", err)
		return
	}

	for _, event := range events {
		if err := d.dispatchEvent(ctx, event); err != nil {
			log.Printf("outbox dispatcher: event %s: %v", event.ID, err)
		}
	}
}

func (d *OutboxDispatcher) dispatchEvent(ctx context.Context, event *model.OutboxEvent) error {
	request := PublishRequest{
		EventID:     event.ID,
		AggregateID: event.AggregateID,
		EventType:   event.EventType,
		Payload:     event.Payload,
	}

	if err := d.publisher.Publish(ctx, request); err != nil {
		if event.RetryCount+1 >= maxPublishAttempts {
			if failErr := d.store.MarkFailed(ctx, event.ID); failErr != nil {
				return failErr
			}
			log.Printf("outbox dispatcher: event %s permanently FAILED after %d attempts: %v", event.ID, event.RetryCount+1, err)
			return err
		}
		nextRetryAt := time.Now().Add(retryDelay(event.RetryCount))
		if retryErr := d.store.MarkRetry(ctx, event.ID, nextRetryAt); retryErr != nil {
			return retryErr
		}
		return err
	}

	return d.store.MarkProcessed(ctx, event.ID)
}

func retryDelay(retryCount int) time.Duration {
	if retryCount < 0 {
		retryCount = 0
	}

	delay := baseRetryDelay
	for attempt := 0; attempt < retryCount && delay < maxRetryDelay; attempt++ {
		delay *= 2
	}

	if delay > maxRetryDelay {
		return maxRetryDelay
	}
	return delay
}
