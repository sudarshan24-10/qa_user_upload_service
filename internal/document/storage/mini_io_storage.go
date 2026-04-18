package storage

import (
	"context"
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

func (s *MiniIOStorage) DeleteObject(ctx context.Context, objectKey string) error {
	return s.Client.RemoveObject(ctx, s.BucketName, objectKey, minio.RemoveObjectOptions{})
}
