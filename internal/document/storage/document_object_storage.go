package storage

import (
	"context"
	"time"
)

// ObjectInfo describes a stored object as it actually exists in the bucket.
type ObjectInfo struct {
	Size int64
	Head []byte // first bytes of the object, used for content sniffing
}

type ObjectStorage interface {
	GenerateUploadURL(ctx context.Context, objectKey string, contentType string, expiry time.Duration) (string, error)
	CheckObjectExists(ctx context.Context, objectKey string) (bool, error)
	// InspectObject returns (nil, nil) when the object does not exist.
	InspectObject(ctx context.Context, objectKey string, headBytes int64) (*ObjectInfo, error)
	DeleteObject(ctx context.Context, objectKey string) error
}
