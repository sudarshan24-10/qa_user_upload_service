package storage

import (
	"context"
	"time"
)

type ObjectStorage interface {
	GenerateUploadURL(ctx context.Context, objectKey string, contentType string, expiry time.Duration) (string, error)
	CheckObjectExists(ctx context.Context, objectKey string) (bool, error)
	DeleteObject(ctx context.Context, objectKey string) error
}
