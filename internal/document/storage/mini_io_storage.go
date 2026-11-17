package storage

import (
	"context"
	"io"
	"time"

	"github.com/minio/minio-go/v7"
)

type MiniIOStorage struct {
	Client     *minio.Client
	BucketName string
}

func NewMiniIOStorage(client *minio.Client, bucketName string) *MiniIOStorage {
	return &MiniIOStorage{
		Client:     client,
		BucketName: bucketName,
	}
}

func (s *MiniIOStorage) GenerateUploadURL(ctx context.Context, objectKey string, contentType string, expiry time.Duration) (string, error) {
	url, err := s.Client.PresignedPutObject(ctx, s.BucketName, objectKey, expiry)
	if err != nil {
		return "", err
	}
	return url.String(), nil
}

func (s *MiniIOStorage) CheckObjectExists(ctx context.Context, objectKey string) (bool, error) {
	_, err := s.Client.StatObject(ctx, s.BucketName, objectKey, minio.StatObjectOptions{})
	if err != nil {
		errResp, ok := err.(minio.ErrorResponse)
		if ok && errResp.Code == "NoSuchKey" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// InspectObject stats the object and reads up to headBytes from its start.
// Returns (nil, nil) when the object does not exist.
func (s *MiniIOStorage) InspectObject(ctx context.Context, objectKey string, headBytes int64) (*ObjectInfo, error) {
	stat, err := s.Client.StatObject(ctx, s.BucketName, objectKey, minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, nil
		}
		return nil, err
	}

	n := headBytes
	if stat.Size < n {
		n = stat.Size
	}

	head := make([]byte, n)
	if n > 0 {
		opts := minio.GetObjectOptions{}
		if err := opts.SetRange(0, n-1); err != nil {
			return nil, err
		}
		obj, err := s.Client.GetObject(ctx, s.BucketName, objectKey, opts)
		if err != nil {
			return nil, err
		}
		defer obj.Close()
		if _, err := io.ReadFull(obj, head); err != nil {
			return nil, err
		}
	}

	return &ObjectInfo{Size: stat.Size, Head: head}, nil
}

func (s *MiniIOStorage) DeleteObject(ctx context.Context, objectKey string) error {
	return s.Client.RemoveObject(ctx, s.BucketName, objectKey, minio.RemoveObjectOptions{})
}
