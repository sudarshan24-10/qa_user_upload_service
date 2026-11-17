package outbox_dispatcher

import "context"

// PublishRequest is the broker-agnostic event envelope used by the dispatcher.
type PublishRequest struct {
	EventID     string
	AggregateID string
	EventType   string
	Payload     string
}

// Publisher abstracts queue backends (Redis Streams, RabbitMQ, etc.).
type Publisher interface {
	Publish(ctx context.Context, req PublishRequest) error
	Close(ctx context.Context) error
}
