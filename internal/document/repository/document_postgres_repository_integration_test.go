package repository

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/model"
)

// Integration tests for the SQL guards of TransitionExpiredUploadToFailed (1A) and
// TransitionInitiatedToFailed (1B) against a real Postgres. They skip when POSTGRES_URL
// is absent or Postgres is unreachable. Each test inserts its own throwaway row and
// deletes exactly that row afterwards; existing data is never read or modified.
// Values are never logged.

func newIntegrationRepo(t *testing.T) (*PostgresRepository, *pgxpool.Pool) {
	t.Helper()
	_ = godotenv.Load("../../../.env")

	url := os.Getenv("POSTGRES_URL")
	if url == "" {
		t.Skip("POSTGRES_URL not set; skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second) // remote DBs can be slow on first connect
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Skip("cannot create Postgres pool (details suppressed); skipping integration test")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skip("Postgres unreachable (details suppressed); skipping integration test")
	}
	t.Cleanup(pool.Close)

	return NewPostgresRepository(pool), pool
}

// insertThrowawayDoc inserts one row and registers deletion of exactly that row.
// expired=true sets upload_expires_at one hour in the past (computed by the DB clock).
func insertThrowawayDoc(t *testing.T, pool *pgxpool.Pool, status string, expired bool) string {
	t.Helper()
	id := uuid.New().String()
	expiry := "NOW() + interval '1 hour'"
	if expired {
		expiry = "NOW() - interval '1 hour'"
	}

	_, err := pool.Exec(context.Background(), `
		INSERT INTO documents (id, user_id, object_key, file_name, size, status,
		                       idempotency_key, content_type, upload_expires_at)
		VALUES ($1, $2, $3, 'it.pdf', 1, $4::document_status, $5, 'application/pdf', `+expiry+`)`,
		id, uuid.New().String(), "integration-test/"+id, status, "it-"+id)
	if err != nil {
		t.Fatalf("insert throwaway document failed (details suppressed)")
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM documents WHERE id = $1`, id)
	})
	return id
}

type docRow struct {
	status        string
	failureStage  *string
	failureReason *string
	failedAtSet   bool
}

func readDoc(t *testing.T, pool *pgxpool.Pool, id string) docRow {
	t.Helper()
	var r docRow
	err := pool.QueryRow(context.Background(), `
		SELECT status::text, failure_stage, failure_reason, failed_at IS NOT NULL
		FROM documents WHERE id = $1`, id).Scan(&r.status, &r.failureStage, &r.failureReason, &r.failedAtSet)
	if err != nil {
		t.Fatalf("read throwaway document failed (details suppressed)")
	}
	return r
}

// runExpiryTransition executes the 1A method in its own transaction and commits on success.
func runExpiryTransition(t *testing.T, repo *PostgresRepository, id string) error {
	t.Helper()
	ctx := context.Background()
	tx, err := repo.BeginTx(ctx)
	if err != nil {
		t.Fatalf("begin tx failed")
	}
	defer tx.Rollback(ctx)

	if err := repo.TransitionExpiredUploadToFailed(ctx, tx, id, model.FailureStageUpload, "upload expired"); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit failed")
	}
	return nil
}

// ---------- 1A: TransitionExpiredUploadToFailed ----------

func TestTransitionExpiredUploadToFailed_Integration_ExpiredInitiatedBecomesFailed(t *testing.T) {
	repo, pool := newIntegrationRepo(t)
	id := insertThrowawayDoc(t, pool, "INITIATED", true)

	if err := runExpiryTransition(t, repo, id); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	r := readDoc(t, pool, id)
	if r.status != "FAILED" || !r.failedAtSet {
		t.Fatalf("expected FAILED with failed_at set, got %+v", r)
	}
	if r.failureStage == nil || *r.failureStage != "UPLOAD" || r.failureReason == nil || *r.failureReason != "upload expired" {
		t.Fatalf("unexpected failure fields: %+v", r)
	}
}

func TestTransitionExpiredUploadToFailed_Integration_NotYetExpiredIsUntouched(t *testing.T) {
	repo, pool := newIntegrationRepo(t)
	id := insertThrowawayDoc(t, pool, "INITIATED", false)

	err := runExpiryTransition(t, repo, id)

	var invalid *model.InvalidStateTransitionError
	if !errors.As(err, &invalid) {
		t.Fatalf("expected InvalidStateTransitionError, got %v", err)
	}
	if r := readDoc(t, pool, id); r.status != "INITIATED" || r.failedAtSet {
		t.Fatalf("document must be untouched, got %+v", r)
	}
}

// This is the race from the review: a stale "expired" candidate that has since been completed.
func TestTransitionExpiredUploadToFailed_Integration_UploadedDocIsNotFailed(t *testing.T) {
	repo, pool := newIntegrationRepo(t)
	id := insertThrowawayDoc(t, pool, "UPLOADED", true)

	err := runExpiryTransition(t, repo, id)

	var invalid *model.InvalidStateTransitionError
	if !errors.As(err, &invalid) || invalid.CurrentStatus != model.StatusUploaded {
		t.Fatalf("expected InvalidStateTransitionError from UPLOADED, got %v", err)
	}
	if r := readDoc(t, pool, id); r.status != "UPLOADED" || r.failureStage != nil || r.failedAtSet {
		t.Fatalf("UPLOADED document must be untouched, got %+v", r)
	}
}

func TestTransitionExpiredUploadToFailed_Integration_ProcessingDocIsNotFailed(t *testing.T) {
	repo, pool := newIntegrationRepo(t)
	id := insertThrowawayDoc(t, pool, "PROCESSING", true)

	err := runExpiryTransition(t, repo, id)

	var invalid *model.InvalidStateTransitionError
	if !errors.As(err, &invalid) {
		t.Fatalf("expected InvalidStateTransitionError, got %v", err)
	}
	if r := readDoc(t, pool, id); r.status != "PROCESSING" {
		t.Fatalf("PROCESSING document must be untouched, got %+v", r)
	}
}

func TestTransitionExpiredUploadToFailed_Integration_UnknownDocumentNotFound(t *testing.T) {
	repo, _ := newIntegrationRepo(t)

	err := runExpiryTransition(t, repo, uuid.New().String())

	var notFound *model.DocumentNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected DocumentNotFoundError, got %v", err)
	}
}

// ---------- 1B: TransitionInitiatedToFailed ----------

func TestTransitionInitiatedToFailed_Integration_InitiatedBecomesFailed(t *testing.T) {
	repo, pool := newIntegrationRepo(t)
	id := insertThrowawayDoc(t, pool, "INITIATED", false)

	changed, err := repo.TransitionInitiatedToFailed(context.Background(), id, model.FailureStageUpload, "file not found in storage during completion")

	if err != nil || !changed {
		t.Fatalf("expected changed=true, got changed=%v err=%v", changed, err)
	}
	r := readDoc(t, pool, id)
	if r.status != "FAILED" || !r.failedAtSet || r.failureStage == nil || *r.failureStage != "UPLOAD" ||
		r.failureReason == nil || *r.failureReason != "file not found in storage during completion" {
		t.Fatalf("unexpected row after fail: %+v", r)
	}
}

// The race from the review: a concurrent complete already moved the doc to UPLOADED.
func TestTransitionInitiatedToFailed_Integration_UploadedIsNotOverwritten(t *testing.T) {
	repo, pool := newIntegrationRepo(t)
	id := insertThrowawayDoc(t, pool, "UPLOADED", false)

	changed, err := repo.TransitionInitiatedToFailed(context.Background(), id, model.FailureStageUpload, "x")

	if err != nil || changed {
		t.Fatalf("expected changed=false, got changed=%v err=%v", changed, err)
	}
	if r := readDoc(t, pool, id); r.status != "UPLOADED" || r.failureStage != nil || r.failedAtSet {
		t.Fatalf("UPLOADED document must be untouched, got %+v", r)
	}
}

func TestTransitionInitiatedToFailed_Integration_ProcessingIsNotOverwritten(t *testing.T) {
	repo, pool := newIntegrationRepo(t)
	id := insertThrowawayDoc(t, pool, "PROCESSING", false)

	changed, err := repo.TransitionInitiatedToFailed(context.Background(), id, model.FailureStageUpload, "x")

	if err != nil || changed {
		t.Fatalf("expected changed=false, got changed=%v err=%v", changed, err)
	}
	if r := readDoc(t, pool, id); r.status != "PROCESSING" {
		t.Fatalf("PROCESSING document must be untouched, got %+v", r)
	}
}

func TestTransitionInitiatedToFailed_Integration_AlreadyFailedKeepsOriginalReason(t *testing.T) {
	repo, pool := newIntegrationRepo(t)
	id := insertThrowawayDoc(t, pool, "INITIATED", false)
	if changed, err := repo.TransitionInitiatedToFailed(context.Background(), id, model.FailureStageUpload, "first"); err != nil || !changed {
		t.Fatalf("setup failed: changed=%v err=%v", changed, err)
	}

	changed, err := repo.TransitionInitiatedToFailed(context.Background(), id, model.FailureStageUpload, "second")

	if err != nil || changed {
		t.Fatalf("expected changed=false, got changed=%v err=%v", changed, err)
	}
	if r := readDoc(t, pool, id); r.failureReason == nil || *r.failureReason != "first" {
		t.Fatalf("original failure reason must be preserved, got %+v", r)
	}
}

func TestTransitionInitiatedToFailed_Integration_UnknownDocumentNoChange(t *testing.T) {
	repo, _ := newIntegrationRepo(t)

	changed, err := repo.TransitionInitiatedToFailed(context.Background(), uuid.New().String(), model.FailureStageUpload, "x")

	if err != nil || changed {
		t.Fatalf("expected (false, nil), got changed=%v err=%v", changed, err)
	}
}
