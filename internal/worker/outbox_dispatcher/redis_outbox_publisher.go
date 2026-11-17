package outbox_dispatcher

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/config"
)

type RedisOutboxPublisher struct {
	client *redis.Client
	stream string
}

func NewRedisOutboxPublisher(cfg *config.Config) (*RedisOutboxPublisher, error) {
	options, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Redis URL: %w", err)
	}

	client := redis.NewClient(options)
	return &RedisOutboxPublisher{
		client: client,
		stream: cfg.RedisStream,
	}, nil
}

func (p *RedisOutboxPublisher) Publish(ctx context.Context, req PublishRequest) error {

	_, err := p.client.XAdd(ctx, &redis.XAddArgs{
		Stream: p.stream,
		Values: map[string]any{
			"event_id":     req.EventID,
			"aggregate_id": req.AggregateID,
			"event_type":   req.EventType,
			"payload":      req.Payload,
		},
	}).Result()

	if err != nil {
		return fmt.Errorf("failed to publish event to Redis stream: %w", err)
	}

	return nil
}

func (p *RedisOutboxPublisher) Close(ctx context.Context) error {
	if err := p.client.Close(); err != nil {
		return fmt.Errorf("failed to close Redis client: %w", err)
	}
	return nil
}
