package storage

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"os"
)

// Integration tests against a real MinIO. They skip when the required env var
// names are absent or MinIO is unreachable. Values are never logged.
// Required names: MINIO_ENDPOINT, OBJECT_STORAGE_ACCESS_KEY, OBJECT_STORAGE_SECRET_KEY, BUCKET_NAME.

func newIntegrationStorage(t *testing.T) *MiniIOStorage {
	t.Helper()
	_ = godotenv.Load("../../../.env")

	endpoint := os.Getenv("MINIO_ENDPOINT")
	accessKey := os.Getenv("OBJECT_STORAGE_ACCESS_KEY")
	secretKey := os.Getenv("OBJECT_STORAGE_SECRET_KEY")
	bucket := os.Getenv("BUCKET_NAME")
	if endpoint == "" || accessKey == "" || secretKey == "" || bucket == "" {
		t.Skip("MinIO env vars not set; skipping integration test")
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: false,
	})
	if err != nil {
		t.Skip("cannot create MinIO client (details suppressed); skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ok, err := client.BucketExists(ctx, bucket)
	if err != nil || !ok {
		t.Skip("MinIO unreachable or bucket missing (details suppressed); skipping integration test")
	}

	return NewMiniIOStorage(client, bucket)
}

// putThrowaway uploads data under a unique throwaway key and registers cleanup.
func putThrowaway(t *testing.T, s *MiniIOStorage, data []byte) string {
	t.Helper()
	key := "integration-test/" + uuid.New().String() + "/object.bin"
	ctx := context.Background()
	_, err := s.Client.PutObject(ctx, s.BucketName, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{})
	if err != nil {
		t.Fatalf("put throwaway object failed (details suppressed)")
	}
	t.Cleanup(func() { _ = s.DeleteObject(context.Background(), key) })
	return key
}

func TestInspectObject_Integration_MissingObject(t *testing.T) {
	s := newIntegrationStorage(t)
	key := "integration-test/" + uuid.New().String() + "/does-not-exist.bin"

	info, err := s.InspectObject(context.Background(), key, 1024)

	if err != nil || info != nil {
		t.Fatalf("expected (nil, nil) for missing object, got info=%v err=%v", info, err)
	}
}

func TestInspectObject_Integration_SmallObjectUnderHeadLimit(t *testing.T) {
	s := newIntegrationStorage(t)
	data := []byte("%PDF-1.4 tiny")
	key := putThrowaway(t, s, data)

	info, err := s.InspectObject(context.Background(), key, 1024)

	if err != nil || info == nil {
		t.Fatalf("unexpected result info=%v err=%v", info, err)
	}
	if info.Size != int64(len(data)) {
		t.Fatalf("expected size %d, got %d", len(data), info.Size)
	}
	if !bytes.Equal(info.Head, data) {
		t.Fatalf("expected head to equal whole small object")
	}
}

func TestInspectObject_Integration_LargeObjectReadsOnlyHead(t *testing.T) {
	s := newIntegrationStorage(t)
	data := make([]byte, 5000)
	for i := range data {
		data[i] = byte(i % 251)
	}
	key := putThrowaway(t, s, data)

	info, err := s.InspectObject(context.Background(), key, 1024)

	if err != nil || info == nil {
		t.Fatalf("unexpected result info=%v err=%v", info, err)
	}
	if info.Size != 5000 {
		t.Fatalf("expected size 5000, got %d", info.Size)
	}
	if len(info.Head) != 1024 {
		t.Fatalf("expected head length 1024, got %d", len(info.Head))
	}
	if !bytes.Equal(info.Head, data[:1024]) {
		t.Fatalf("head bytes do not match first 1024 bytes of object")
	}
}

func TestInspectObject_Integration_HeadLimitEqualsSize(t *testing.T) {
	s := newIntegrationStorage(t)
	data := bytes.Repeat([]byte{0xAB}, 64)
	key := putThrowaway(t, s, data)

	info, err := s.InspectObject(context.Background(), key, 64)

	if err != nil || info == nil || info.Size != 64 || !bytes.Equal(info.Head, data) {
		t.Fatalf("unexpected result info=%v err=%v", info, err)
	}
}

func TestInspectObject_Integration_ZeroByteObject(t *testing.T) {
	s := newIntegrationStorage(t)
	key := putThrowaway(t, s, []byte{})

	info, err := s.InspectObject(context.Background(), key, 1024)

	if err != nil || info == nil {
		t.Fatalf("unexpected result info=%v err=%v", info, err)
	}
	if info.Size != 0 || len(info.Head) != 0 {
		t.Fatalf("expected empty object, got size=%d head=%d", info.Size, len(info.Head))
	}
}

func TestInspectObject_Integration_PresignedPutThenInspect(t *testing.T) {
	s := newIntegrationStorage(t)
	ctx := context.Background()
	key := "integration-test/" + uuid.New().String() + "/presigned.pdf"
	t.Cleanup(func() { _ = s.DeleteObject(context.Background(), key) })

	url, err := s.GenerateUploadURL(ctx, key, "application/pdf", time.Minute)
	if err != nil {
		t.Fatalf("GenerateUploadURL failed (details suppressed)")
	}

	body := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("x"), 2000)...)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("building PUT request failed")
	}
	req.Header.Set("Content-Type", "application/pdf")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("presigned PUT failed (details suppressed)")
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("presigned PUT returned status %d", resp.StatusCode)
	}

	exists, err := s.CheckObjectExists(ctx, key)
	if err != nil || !exists {
		t.Fatalf("CheckObjectExists after presigned PUT: exists=%v err=%v", exists, err)
	}

	info, err := s.InspectObject(ctx, key, 1024)
	if err != nil || info == nil {
		t.Fatalf("unexpected result info=%v err=%v", info, err)
	}
	if info.Size != int64(len(body)) {
		t.Fatalf("expected size %d, got %d", len(body), info.Size)
	}
	if !bytes.HasPrefix(info.Head, []byte("%PDF-")) || len(info.Head) != 1024 {
		t.Fatalf("unexpected head (len=%d)", len(info.Head))
	}
}
