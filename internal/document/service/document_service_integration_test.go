package service

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/joho/godotenv"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/config"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/storage"
)

// Integration test against a real MinIO. Skips when the required env var names are
// absent or MinIO is unreachable. Values are never logged.
// Required names: MINIO_ENDPOINT, OBJECT_STORAGE_ACCESS_KEY, OBJECT_STORAGE_SECRET_KEY, BUCKET_NAME.

func newIntegrationMiniIO(t *testing.T) *storage.MiniIOStorage {
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

	return storage.NewMiniIOStorage(client, bucket)
}

func TestInitiateUpload_Integration_SanitizedKeyWorksWithRealMinIO(t *testing.T) {
	store := newIntegrationMiniIO(t)
	svc := NewDocumentService(&fakeDocRepo{}, &fakeOutboxRepo{}, validator.New(), store, &config.Config{MAX_SIZE: 25 * 1024 * 1024})

	res, err := svc.InitiateUpload(context.Background(), initiateReq("../x/my report #1?.pdf"))
	if err != nil {
		t.Fatalf("initiate failed (details suppressed)")
	}
	t.Cleanup(func() { _ = store.DeleteObject(context.Background(), res.ObjectKey) })

	if !strings.HasSuffix(res.ObjectKey, "/my_report__1_.pdf") {
		t.Fatalf("unexpected key shape")
	}

	body := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("x"), 100)...)
	req, err := http.NewRequest(http.MethodPut, res.UploadURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("building PUT request failed")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("presigned PUT failed (details suppressed)")
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("presigned PUT returned status %d", resp.StatusCode)
	}

	info, err := store.InspectObject(context.Background(), res.ObjectKey, 1024)
	if err != nil || info == nil || info.Size != int64(len(body)) {
		t.Fatalf("inspect after upload: info=%v err=%v", info, err)
	}
	if !bytes.HasPrefix(info.Head, []byte("%PDF-")) {
		t.Fatalf("unexpected head")
	}
}
