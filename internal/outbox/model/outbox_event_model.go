package model

import "time"

type Status string

const (
	StatusPending   Status = "PENDING"
	StatusProcessed Status = "PROCESSED"
	StatusFailed    Status = "FAILED"
)

type OutboxEvent struct {
	ID          string
	AggregateID string
	EventType   string
	Payload     string
	Status      Status
	RetryCount  int
	NextRetryAt time.Time
	CreatedAt   time.Time
	ProcessedAt *time.Time
}
