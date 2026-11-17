package service

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/jackc/pgx/v5"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/config"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/dto"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/model"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/repository"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/storage"
	outboxModel "github.com/sudarshan24-10/qa_user_upload_service/internal/outbox/model"
)

const (
	testDocID  = "11111111-1111-1111-1111-111111111111"
	testUserID = "22222222-2222-2222-2222-222222222222"
)

// fakeDocRepo embeds the interface so only the methods under test are implemented.
// Any other call hits the nil embedded interface and panics, which fails the test
// (this also guards against the unguarded UpdateStatus being used again).
type fakeDocRepo struct {
	repository.DocumentRepository
	doc          *model.Document
	createdDoc   *model.Document // if set, Create returns it (simulates an idempotent retry)
	created      *model.Document // last document passed to Create
	createCalls  int
	changed      bool
	failErr      error
	failCalls    int
	failStage    model.FailureStage
	failReason   string
	failedDocID  string
	beginTxCalls int
}

func (f *fakeDocRepo) Create(ctx context.Context, doc *model.Document) (*model.Document, error) {
	f.createCalls++
	f.created = doc
	if f.createdDoc != nil {
		return f.createdDoc, nil
	}
	return doc, nil
}

func (f *fakeDocRepo) GetByID(ctx context.Context, id string) (*model.Document, error) {
	return f.doc, nil
}

func (f *fakeDocRepo) TransitionInitiatedToFailed(ctx context.Context, documentID string, stage model.FailureStage, reason string) (bool, error) {
	f.failCalls++
	f.failedDocID = documentID
	f.failStage = stage
	f.failReason = reason
	return f.changed, f.failErr
}

func (f *fakeDocRepo) BeginTx(ctx context.Context) (pgx.Tx, error) {
	f.beginTxCalls++
	return nil, errors.New("BeginTx must not be called")
}

type fakeOutboxRepo struct {
	created int
}

func (f *fakeOutboxRepo) Create(ctx context.Context, e *outboxModel.OutboxEvent) error { return nil }
func (f *fakeOutboxRepo) CreateWithTx(ctx context.Context, tx pgx.Tx, e *outboxModel.OutboxEvent) error {
	f.created++
	return nil
}
func (f *fakeOutboxRepo) FetchPending(ctx context.Context, limit int) ([]*outboxModel.OutboxEvent, error) {
	return nil, nil
}
func (f *fakeOutboxRepo) ClaimPending(ctx context.Context, limit int, lease time.Duration) ([]*outboxModel.OutboxEvent, error) {
	return nil, nil
}
func (f *fakeOutboxRepo) MarkFailed(ctx context.Context, id string) error { return nil }
func (f *fakeOutboxRepo) MarkProcessed(ctx context.Context, id string) error { return nil }
func (f *fakeOutboxRepo) MarkRetry(ctx context.Context, id string, t time.Time) error {
	return nil
}

// fakeStorage returns info for InspectObject; a nil info means the object is missing.
type fakeStorage struct {
	info           *storage.ObjectInfo
	err            error
	lastPresignKey string
}

func (f *fakeStorage) GenerateUploadURL(ctx context.Context, k string, ct string, e time.Duration) (string, error) {
	f.lastPresignKey = k
	return "http://example.invalid/upload", nil
}
func (f *fakeStorage) CheckObjectExists(ctx context.Context, k string) (bool, error) {
	return f.info != nil, f.err
}
func (f *fakeStorage) InspectObject(ctx context.Context, k string, n int64) (*storage.ObjectInfo, error) {
	return f.info, f.err
}
func (f *fakeStorage) DeleteObject(ctx context.Context, k string) error { return nil }

func newTestService(repo *fakeDocRepo, outbox *fakeOutboxRepo, info *storage.ObjectInfo) *documentService {
	return newTestServiceWithStorage(repo, outbox, &fakeStorage{info: info})
}

func newTestServiceWithStorage(repo *fakeDocRepo, outbox *fakeOutboxRepo, store *fakeStorage) *documentService {
	return NewDocumentService(repo, outbox, validator.New(), store, &config.Config{MAX_SIZE: 25 * 1024 * 1024})
}

func initiatedDoc() *model.Document {
	return &model.Document{
		ID:             testDocID,
		UserID:         testUserID,
		ObjectKey:      testUserID + "/" + testDocID + "/a.pdf",
		Status:         model.StatusInitiated,
		IdempotencyKey: "key-1",
		Size:           1000,
		ContentType:    "application/pdf",
	}
}

func pdfInfo(size int64) *storage.ObjectInfo {
	return &storage.ObjectInfo{Size: size, Head: []byte("%PDF-1.7\n%binary")}
}

func completeReq() dto.CompleteUploadRequest {
	return dto.CompleteUploadRequest{DocumentID: testDocID, UserID: testUserID, IdempotencyKey: "key-1"}
}

func assertFileNotFound(t *testing.T, err error) {
	t.Helper()
	var nf *model.FileNotFoundInStorageError
	if !errors.As(err, &nf) {
		t.Fatalf("expected FileNotFoundInStorageError, got %v", err)
	}
}

func TestCompleteUpload_MissingFile_FailsInitiatedDocument(t *testing.T) {
	repo := &fakeDocRepo{doc: initiatedDoc(), changed: true}
	svc := newTestService(repo, &fakeOutboxRepo{}, nil)

	_, err := svc.CompleteUpload(context.Background(), completeReq())

	assertFileNotFound(t, err)
	if repo.failCalls != 1 || repo.failedDocID != testDocID {
		t.Fatalf("expected one guarded fail call for %s, got %d for %q", testDocID, repo.failCalls, repo.failedDocID)
	}
	if repo.failStage != model.FailureStageUpload {
		t.Fatalf("expected stage %s, got %s", model.FailureStageUpload, repo.failStage)
	}
	if repo.failReason != "file not found in storage during completion" {
		t.Fatalf("unexpected reason %q", repo.failReason)
	}
}

func TestCompleteUpload_MissingFile_ConcurrentStateChange(t *testing.T) {
	repo := &fakeDocRepo{doc: initiatedDoc(), changed: false}
	outbox := &fakeOutboxRepo{}
	svc := newTestService(repo, outbox, nil)

	_, err := svc.CompleteUpload(context.Background(), completeReq())

	assertFileNotFound(t, err)
	if repo.beginTxCalls != 0 || outbox.created != 0 {
		t.Fatalf("expected no tx/outbox activity, got tx=%d outbox=%d", repo.beginTxCalls, outbox.created)
	}
}

func TestCompleteUpload_MissingFile_RepoErrorStillReturnsFileNotFound(t *testing.T) {
	repo := &fakeDocRepo{doc: initiatedDoc(), failErr: errors.New("db down")}
	svc := newTestService(repo, &fakeOutboxRepo{}, nil)

	_, err := svc.CompleteUpload(context.Background(), completeReq())

	assertFileNotFound(t, err)
}

func TestFinalizeUpload_MissingFile_GuardedFailure(t *testing.T) {
	for _, changed := range []bool{true, false} {
		repo := &fakeDocRepo{doc: initiatedDoc(), changed: changed}
		outbox := &fakeOutboxRepo{}
		svc := newTestService(repo, outbox, nil)

		err := svc.FinalizeUpload(context.Background(), testDocID)

		assertFileNotFound(t, err)
		if repo.failCalls != 1 || repo.failStage != model.FailureStageUpload {
			t.Fatalf("changed=%v: expected one UPLOAD-stage fail call, got %d (%s)", changed, repo.failCalls, repo.failStage)
		}
		if repo.failReason != "file not found in storage during recovery" {
			t.Fatalf("unexpected reason %q", repo.failReason)
		}
		if repo.beginTxCalls != 0 || outbox.created != 0 {
			t.Fatalf("changed=%v: expected no tx/outbox activity", changed)
		}
	}
}

func assertGuardedUploadFailure(t *testing.T, repo *fakeDocRepo, outbox *fakeOutboxRepo) {
	t.Helper()
	if repo.failCalls != 1 || repo.failStage != model.FailureStageUpload {
		t.Fatalf("expected one UPLOAD-stage guarded fail call, got %d (%s)", repo.failCalls, repo.failStage)
	}
	if repo.beginTxCalls != 0 || outbox.created != 0 {
		t.Fatalf("expected no tx/outbox activity, got tx=%d outbox=%d", repo.beginTxCalls, outbox.created)
	}
}

func TestCompleteUpload_SizeMismatch_FailsDocument(t *testing.T) {
	repo := &fakeDocRepo{doc: initiatedDoc(), changed: true}
	outbox := &fakeOutboxRepo{}
	svc := newTestService(repo, outbox, pdfInfo(2000)) // declared 1000

	_, err := svc.CompleteUpload(context.Background(), completeReq())

	var mismatch *model.FileSizeMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("expected FileSizeMismatchError, got %v", err)
	}
	if mismatch.Declared != 1000 || mismatch.Actual != 2000 {
		t.Fatalf("unexpected sizes: %+v", mismatch)
	}
	assertGuardedUploadFailure(t, repo, outbox)
}

func TestCompleteUpload_OversizedObject_Rejected(t *testing.T) {
	repo := &fakeDocRepo{doc: initiatedDoc(), changed: true}
	outbox := &fakeOutboxRepo{}
	svc := newTestService(repo, outbox, pdfInfo(25*1024*1024+1))

	_, err := svc.CompleteUpload(context.Background(), completeReq())

	var exceeded *model.FileSizeExceededError
	if !errors.As(err, &exceeded) {
		t.Fatalf("expected FileSizeExceededError, got %v", err)
	}
	assertGuardedUploadFailure(t, repo, outbox)
}

func TestCompleteUpload_PDFMagicMismatch_Rejected(t *testing.T) {
	repo := &fakeDocRepo{doc: initiatedDoc(), changed: true}
	outbox := &fakeOutboxRepo{}
	info := &storage.ObjectInfo{Size: 1000, Head: []byte("MZ\x90\x00 not a pdf")}
	svc := newTestService(repo, outbox, info)

	_, err := svc.CompleteUpload(context.Background(), completeReq())

	var mismatch *model.FileContentMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("expected FileContentMismatchError, got %v", err)
	}
	assertGuardedUploadFailure(t, repo, outbox)
}

func TestCompleteUpload_JPEGMagic_PassesVerification(t *testing.T) {
	doc := initiatedDoc()
	doc.ContentType = "image/jpeg"
	repo := &fakeDocRepo{doc: doc, changed: true}
	info := &storage.ObjectInfo{Size: 1000, Head: []byte{0xFF, 0xD8, 0xFF, 0xE0}}
	svc := newTestService(repo, &fakeOutboxRepo{}, info)

	_, err := svc.CompleteUpload(context.Background(), completeReq())

	// Verification passed, so the flow proceeds to BeginTx (the fake makes it fail).
	if repo.failCalls != 0 || repo.beginTxCalls != 1 {
		t.Fatalf("expected verification to pass and tx to start, fail=%d tx=%d err=%v", repo.failCalls, repo.beginTxCalls, err)
	}
}

func TestCompleteUpload_PDFWithPreamble_PassesVerification(t *testing.T) {
	repo := &fakeDocRepo{doc: initiatedDoc(), changed: true}
	head := append(bytes.Repeat([]byte{' '}, 100), []byte("%PDF-1.4")...)
	svc := newTestService(repo, &fakeOutboxRepo{}, &storage.ObjectInfo{Size: 1000, Head: head})

	_, _ = svc.CompleteUpload(context.Background(), completeReq())

	if repo.failCalls != 0 || repo.beginTxCalls != 1 {
		t.Fatalf("expected verification to pass, fail=%d tx=%d", repo.failCalls, repo.beginTxCalls)
	}
}

func TestFinalizeUpload_SizeMismatch_Rejected(t *testing.T) {
	repo := &fakeDocRepo{doc: initiatedDoc(), changed: true}
	outbox := &fakeOutboxRepo{}
	svc := newTestService(repo, outbox, pdfInfo(5))

	err := svc.FinalizeUpload(context.Background(), testDocID)

	var mismatch *model.FileSizeMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("expected FileSizeMismatchError, got %v", err)
	}
	assertGuardedUploadFailure(t, repo, outbox)
}

func TestVerifyStoredObject_Table(t *testing.T) {
	svc := newTestService(&fakeDocRepo{}, &fakeOutboxRepo{}, nil)
	max := int64(25 * 1024 * 1024)
	cases := []struct {
		name    string
		docSize int64
		ctype   string
		info    storage.ObjectInfo
		wantErr string // "", "exceeded", "size", "content"
	}{
		{"exact max size ok", max, "application/pdf", storage.ObjectInfo{Size: max, Head: []byte("%PDF-")}, ""},
		{"one over max", max, "application/pdf", storage.ObjectInfo{Size: max + 1, Head: []byte("%PDF-")}, "exceeded"},
		{"size differs", 10, "application/pdf", storage.ObjectInfo{Size: 11, Head: []byte("%PDF-")}, "size"},
		{"empty head pdf", 0, "application/pdf", storage.ObjectInfo{Size: 0, Head: nil}, "content"},
		{"jpeg ok", 4, "image/jpeg", storage.ObjectInfo{Size: 4, Head: []byte{0xFF, 0xD8, 0xFF, 0xDB}}, ""},
		{"jpeg bad header", 4, "image/jpeg", storage.ObjectInfo{Size: 4, Head: []byte("%PDF")}, "content"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := &model.Document{Size: c.docSize, ContentType: c.ctype}
			err := svc.verifyStoredObject(doc, &c.info)
			var exceeded *model.FileSizeExceededError
			var size *model.FileSizeMismatchError
			var content *model.FileContentMismatchError
			switch c.wantErr {
			case "":
				if err != nil {
					t.Fatalf("unexpected error %v", err)
				}
			case "exceeded":
				if !errors.As(err, &exceeded) {
					t.Fatalf("expected FileSizeExceededError, got %v", err)
				}
			case "size":
				if !errors.As(err, &size) {
					t.Fatalf("expected FileSizeMismatchError, got %v", err)
				}
			case "content":
				if !errors.As(err, &content) {
					t.Fatalf("expected FileContentMismatchError, got %v", err)
				}
			}
		})
	}
}

// ---------- 1D: file name sanitizing ----------

func initiateReq(fileName string) dto.InitiateUploadRequest {
	return dto.InitiateUploadRequest{
		UserID: testUserID, FileName: fileName, ContentType: "application/pdf",
		Size: 1000, IdempotencyKey: "key-1",
	}
}

func TestSanitizeFileNameForKey_Table(t *testing.T) {
	long := strings.Repeat("a", 300) + ".pdf"
	cases := []struct{ name, in, want string }{
		{"plain", "report.pdf", "report.pdf"},
		{"unix traversal", "../../etc/passwd", "passwd"},
		{"windows traversal", `..\..\win.pdf`, "win.pdf"},
		{"nested dirs", "a/b/c.pdf", "c.pdf"},
		{"dotdot", "..", "file"},
		{"single dot", ".", "file"},
		{"slash only", "/", "file"},
		{"trailing slash", "dir/", "file"},
		{"url special chars", "my report #1?.pdf", "my_report__1_.pdf"},
		{"non ascii", "r\u00e9sum\u00e9.pdf", "r_sum_.pdf"},
		{"cjk", "\u65e5\u672c\u8a9e.pdf", "___.pdf"},
		{"control chars", "a\x00b\nc.pdf", "a_b_c.pdf"},
		{"leading dots stripped", ".hidden.pdf", "hidden.pdf"},
		{"whitespace only", " ", "_"},
		{"long name keeps extension", long, strings.Repeat("a", 96) + ".pdf"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeFileNameForKey(c.in); got != c.want {
				t.Fatalf("expected %q, got %q", c.want, got)
			}
		})
	}
}

func TestSanitizeFileNameForKey_LongExtensionIsDropped(t *testing.T) {
	got := sanitizeFileNameForKey("a." + strings.Repeat("b", 200))
	if len(got) != 100 {
		t.Fatalf("expected length 100, got %d", len(got))
	}
}

func TestSanitizeFileNameForKey_AlwaysSafeProperties(t *testing.T) {
	hostile := []string{"../../x", "a/../b", "\x00\x01", "\u00e9\\..\\z", strings.Repeat("/", 50), "..", "a b/c d"}
	for _, in := range hostile {
		got := sanitizeFileNameForKey(in)
		if got == "" || strings.ContainsAny(got, `/\`) || strings.HasPrefix(got, ".") || len(got) > 100 {
			t.Fatalf("input %q produced unsafe result %q", in, got)
		}
	}
}

func TestInitiateUpload_ObjectKeyUsesSanitizedName(t *testing.T) {
	repo := &fakeDocRepo{}
	store := &fakeStorage{}
	svc := newTestServiceWithStorage(repo, &fakeOutboxRepo{}, store)

	res, err := svc.InitiateUpload(context.Background(), initiateReq("../../other-user/x/passwd #1.pdf"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := testUserID + "/" + res.DocumentID + "/passwd__1.pdf"
	if res.ObjectKey != want || store.lastPresignKey != want {
		t.Fatalf("expected key %q, got response %q presign %q", want, res.ObjectKey, store.lastPresignKey)
	}
	if strings.Count(res.ObjectKey, "/") != 2 {
		t.Fatalf("key must have exactly two separators, got %q", res.ObjectKey)
	}
}

func TestInitiateUpload_StoresOriginalFileNameInDocument(t *testing.T) {
	repo := &fakeDocRepo{}
	svc := newTestServiceWithStorage(repo, &fakeOutboxRepo{}, &fakeStorage{})
	original := "my report #1?.pdf"

	_, err := svc.InitiateUpload(context.Background(), initiateReq(original))

	if err != nil || repo.created == nil || repo.created.FileName != original {
		t.Fatalf("expected original file name stored unchanged, got %+v err=%v", repo.created, err)
	}
}

func TestInitiateUpload_IdempotentRetryStillComparesOriginalName(t *testing.T) {
	stored := &model.Document{
		ID: testDocID, UserID: testUserID, FileName: "my report #1?.pdf",
		ContentType: "application/pdf", Size: 1000,
		ObjectKey: testUserID + "/" + testDocID + "/my_report__1_.pdf", IdempotencyKey: "key-1",
	}
	repo := &fakeDocRepo{createdDoc: stored}
	svc := newTestServiceWithStorage(repo, &fakeOutboxRepo{}, &fakeStorage{})

	res, err := svc.InitiateUpload(context.Background(), initiateReq("my report #1?.pdf"))
	if err != nil || res.ObjectKey != stored.ObjectKey {
		t.Fatalf("expected retry to return stored key, got %+v err=%v", res, err)
	}

	_, err = svc.InitiateUpload(context.Background(), initiateReq("different.pdf"))
	var reuse *model.IdempotencyKeyReuseError
	if !errors.As(err, &reuse) {
		t.Fatalf("expected IdempotencyKeyReuseError for a different name, got %v", err)
	}
}

func TestInitiateUpload_FileNameTooLong_Rejected(t *testing.T) {
	repo := &fakeDocRepo{}
	svc := newTestServiceWithStorage(repo, &fakeOutboxRepo{}, &fakeStorage{})

	_, err := svc.InitiateUpload(context.Background(), initiateReq(strings.Repeat("a", 256)))

	var ve validator.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %v", err)
	}
	if repo.createCalls != 0 {
		t.Fatalf("Create must not be called for an invalid request")
	}
}

func TestInitiateUpload_FileNameAtLimit_Accepted(t *testing.T) {
	svc := newTestServiceWithStorage(&fakeDocRepo{}, &fakeOutboxRepo{}, &fakeStorage{})

	if _, err := svc.InitiateUpload(context.Background(), initiateReq(strings.Repeat("a", 255))); err != nil {
		t.Fatalf("255-char name must be accepted, got %v", err)
	}
}
