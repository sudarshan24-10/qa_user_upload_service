package model

import (
	"fmt"
	"time"
)

type Status string

const (
	StatusInitiated  Status = "INITIATED"
	StatusUploaded   Status = "UPLOADED"
	StatusProcessing Status = "PROCESSING"
	StatusCompleted  Status = "COMPLETED"
	StatusFailed     Status = "FAILED"
)

type FailureStage string

const (
	FailureStageUpload     FailureStage = "UPLOAD"
	FailureStageProcessing FailureStage = "PROCESSING"
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
	FailureStage    *FailureStage
	FailureReason   *string
	FailedAt        *time.Time
}

// ==================== State Transition Errors ====================

// InvalidStateTransitionError indicates an attempt to transition from an invalid state
type InvalidStateTransitionError struct {
	DocumentID      string
	CurrentStatus   Status
	AttemptedStatus Status
}

func (e *InvalidStateTransitionError) Error() string {
	return fmt.Sprintf("cannot transition from %s to %s for document %s", e.CurrentStatus, e.AttemptedStatus, e.DocumentID)
}

// DocumentNotFoundError indicates document doesn't exist
type DocumentNotFoundError struct {
	DocumentID string
}

func (e *DocumentNotFoundError) Error() string {
	return fmt.Sprintf("document not found: %s", e.DocumentID)
}

type UnauthorizedDocumentError struct {
	DocumentID string
}

func (e *UnauthorizedDocumentError) Error() string {
	return fmt.Sprintf("unauthorized access to document: %s", e.DocumentID)
}

type IdempotencyKeyMismatchError struct {
	DocumentID string
}

func (e *IdempotencyKeyMismatchError) Error() string {
	return fmt.Sprintf("idempotency key does not match document: %s", e.DocumentID)
}

type IdempotencyKeyReuseError struct {
	IdempotencyKey string
}

func (e *IdempotencyKeyReuseError) Error() string {
	return fmt.Sprintf("idempotency key already belongs to a different upload: %s", e.IdempotencyKey)
}

type InvalidFailureStageError struct {
	Stage FailureStage
}

func (e *InvalidFailureStageError) Error() string {
	return fmt.Sprintf("invalid failure stage: %s", e.Stage)
}

type UnsupportedContentTypeError struct {
	ContentType string
}

func (e *UnsupportedContentTypeError) Error() string {
	return fmt.Sprintf("unsupported content type: %s", e.ContentType)
}

type FileSizeExceededError struct {
	Size    int64
	MaxSize int64
}

func (e *FileSizeExceededError) Error() string {
	return fmt.Sprintf("file size %d exceeds maximum allowed size %d", e.Size, e.MaxSize)
}

type InvalidIdentifierError struct {
	Field string
	Value string
}

func (e *InvalidIdentifierError) Error() string {
	return fmt.Sprintf("invalid %s: %s", e.Field, e.Value)
}

// FileNotFoundInStorageError indicates file is not in object storage
type FileNotFoundInStorageError struct {
	ObjectKey string
}

func (e *FileNotFoundInStorageError) Error() string {
	return fmt.Sprintf("file not found in storage: %s", e.ObjectKey)
}

// FileSizeMismatchError indicates the stored object size differs from the declared size
type FileSizeMismatchError struct {
	Declared int64
	Actual   int64
}

func (e *FileSizeMismatchError) Error() string {
	return fmt.Sprintf("file size mismatch: declared %d bytes, stored %d bytes", e.Declared, e.Actual)
}

// FileContentMismatchError indicates the stored object's header does not match the declared content type
type FileContentMismatchError struct {
	ContentType string
}

func (e *FileContentMismatchError) Error() string {
	return fmt.Sprintf("file content does not match declared content type: %s", e.ContentType)
}
