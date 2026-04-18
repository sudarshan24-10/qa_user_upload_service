package model

import "time"

type Status string

const (
	StatusInitiated  Status = "INITIATED"
	StatusUploaded   Status = "UPLOADED"
	StatusProcessing Status = "PROCESSING"
	StatusCompleted  Status = "COMPLETED"
	StatusFailed     Status = "FAILED"
)

type Document struct {
	ID              string
	UserID          string
	ObjectKey       string
	FileName        string
	Size            int64
	Status          Status
	IdempotencyKey  string
	ContentType     string
	Metadata        map[string]string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	UploadExpiresAt time.Time
	FailedReason    *string
}
