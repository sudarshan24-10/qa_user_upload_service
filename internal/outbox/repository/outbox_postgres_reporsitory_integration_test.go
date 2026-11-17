package repository

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// Integration tests for ClaimPending / MarkFailed against a real Postgres.
//
// Safety: ClaimPending claims EVERY due PENDING row in the table and a running dispatcher
// could publish throwaway rows to the real Redis stream. Therefore:
//   - the tests only run when the env var NAME OUTBOX_CLAIM_IT is set (opt-in), and
//   - they skip if any non-throwaway PENDING row is currently due, and
//   - the caller must make sure no dispatcher/API process is running.
//
// Throwaway rows use aggregate_id='integration-test' and are deleted by id afterwards.
// Env values are never logged. Required names: POSTGRES_URL, OUTBOX_CLAIM_IT.

const throwawayAggregate = "integration-test"

func newClaimIntegrationRepo(t *testing.T) (*PostgresOutboxRepository, *pgxpool.Pool) {
	t.Helper()
	_ = godotenv.Load("../../../.env")

	if os.Getenv("OUTBOX_CLAIM_IT") == "" {
		t.Skip("OUTBOX_CLAIM_IT not set; skipping outbox claim integration test")
	}
	url := os.Getenv("POSTGRES_URL")
	if url == "" {
		t.Skip("POSTGRES_URL not set; skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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

	var others int
	err = pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_events
		WHERE status = 'PENDING'
		  AND (next_retry_at IS NULL OR next_retry_at <= NOW())
		  AND aggregate_id <> $1`, throwawayAggregate).Scan(&others)
	if err != nil {
		pool.Close()
		t.Skip("cannot check for existing pending outbox rows (details suppressed); skipping")
	}
	if others > 0 {
		pool.Close()
		t.Skipf("%d non-throwaway PENDING outbox rows are due; refusing to claim them", others)
	}

	return NewPostgresOutboxRepository(pool), pool
}

// insertOutboxRow inserts one throwaway row and registers deletion of exactly that row.
// dueExpr is a SQL expression for next_retry_at ("NULL", "NOW() + interval '1 hour'", ...).
// createdOffsetSec shifts created_at so ordering is deterministic.
func insertOutboxRow(t *testing.T, pool *pgxpool.Pool, status, dueExpr string, createdOffsetSec int) string {
	t.Helper()
	id := uuid.New().String()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO outbox_events (id, aggregate_id, event_type, payload, status, retry_count, next_retry_at, created_at)
		VALUES ($1, $2, 'INTEGRATION_TEST', '{}'::jsonb, $3, 0, `+dueExpr+`, NOW() + ($4 * interval '1 second'))`,
		id, throwawayAggregate, status, createdOffsetSec)
	if err != nil {
		t.Fatalf("insert throwaway outbox row failed (details suppressed)")
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM outbox_events WHERE id = $1`, id)
	})
	return id
}

type outboxRow struct {
	status      string
	retryCount  int
	dueIsNull   bool
	dueInFuture bool
}

func readOutboxRow(t *testing.T, pool *pgxpool.Pool, id string) outboxRow {
	t.Helper()
	var r outboxRow
	err := pool.QueryRow(context.Background(), `
		SELECT status, retry_count, next_retry_at IS NULL, COALESCE(next_retry_at > NOW(), false)
		FROM outbox_events WHERE id = $1`, id).Scan(&r.status, &r.retryCount, &r.dueIsNull, &r.dueInFuture)
	if err != nil {
		t.Fatalf("read throwaway outbox row failed (details suppressed)")
	}
	return r
}

func idSet(ids ...string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func TestClaimPending_Integration_ClaimsDueRowsAndLeasesThem(t *testing.T) {
	repo, pool := newClaimIntegrationRepo(t)
	a := insertOutboxRow(t, pool, "PENDING", "NULL", 0)
	b := insertOutboxRow(t, pool, "PENDING", "NOW() - interval '1 minute'", 1)
	c := insertOutboxRow(t, pool, "PENDING", "NULL", 2)

	claimed, err := repo.ClaimPending(context.Background(), 100, 60*time.Second)
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	want := idSet(a, b, c)
	if len(claimed) != 3 {
		t.Fatalf("expected 3 claimed rows, got %d", len(claimed))
	}
	for _, e := range claimed {
		if !want[e.ID] || e.AggregateID != throwawayAggregate || e.Status != "PENDING" {
			t.Fatalf("unexpected claimed event %+v", e)
		}
	}
	for _, id := range []string{a, b, c} {
		if r := readOutboxRow(t, pool, id); !r.dueInFuture || r.status != "PENDING" {
			t.Fatalf("expected leased PENDING row with future next_retry_at, got %+v", r)
		}
	}

	again, err := repo.ClaimPending(context.Background(), 100, 60*time.Second)
	if err != nil || len(again) != 0 {
		t.Fatalf("expected leased rows to be hidden from a second claim, got %d rows err=%v", len(again), err)
	}
}

func TestClaimPending_Integration_SkipsRowsNotYetDue(t *testing.T) {
	repo, pool := newClaimIntegrationRepo(t)
	future := insertOutboxRow(t, pool, "PENDING", "NOW() + interval '1 hour'", 0)
	due := insertOutboxRow(t, pool, "PENDING", "NULL", 1)

	claimed, err := repo.ClaimPending(context.Background(), 100, 60*time.Second)
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	if len(claimed) != 1 || claimed[0].ID != due {
		t.Fatalf("expected only the due row, got %d rows", len(claimed))
	}
	if r := readOutboxRow(t, pool, future); r.status != "PENDING" || !r.dueInFuture {
		t.Fatalf("future row must be untouched, got %+v", r)
	}
}

func TestClaimPending_Integration_SkipsProcessedAndFailedRows(t *testing.T) {
	repo, pool := newClaimIntegrationRepo(t)
	processed := insertOutboxRow(t, pool, "PROCESSED", "NULL", 0)
	failed := insertOutboxRow(t, pool, "FAILED", "NULL", 1)
	pending := insertOutboxRow(t, pool, "PENDING", "NULL", 2)

	claimed, err := repo.ClaimPending(context.Background(), 100, 60*time.Second)
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	if len(claimed) != 1 || claimed[0].ID != pending {
		t.Fatalf("expected only the PENDING row, got %d rows", len(claimed))
	}
	if r := readOutboxRow(t, pool, processed); r.status != "PROCESSED" {
		t.Fatalf("PROCESSED row changed: %+v", r)
	}
	if r := readOutboxRow(t, pool, failed); r.status != "FAILED" {
		t.Fatalf("FAILED row changed: %+v", r)
	}
}

func TestClaimPending_Integration_ConcurrentClaimersGetDisjointSets(t *testing.T) {
	repo, pool := newClaimIntegrationRepo(t)
	all := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		all = append(all, insertOutboxRow(t, pool, "PENDING", "NULL", i))
	}

	var wg sync.WaitGroup
	results := make([][]string, 2)
	errs := make([]error, 2)
	start := make(chan struct{})
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			events, err := repo.ClaimPending(context.Background(), 20, 60*time.Second)
			errs[w] = err
			for _, e := range events {
				results[w] = append(results[w], e.ID)
			}
		}(w)
	}
	close(start)
	wg.Wait()

	for w, err := range errs {
		if err != nil {
			t.Fatalf("claimer %d failed: %v", w, err)
		}
	}
	seen := map[string]int{}
	for _, ids := range results {
		for _, id := range ids {
			seen[id]++
		}
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("event %s was claimed %d times", id, n)
		}
	}
	for _, id := range all {
		if seen[id] != 1 {
			t.Fatalf("event %s was not claimed exactly once (claimer sizes %d and %d)", id, len(results[0]), len(results[1]))
		}
	}
}

func TestClaimPending_Integration_RespectsLimitAndOrdersOldestFirst(t *testing.T) {
	repo, pool := newClaimIntegrationRepo(t)
	newest := insertOutboxRow(t, pool, "PENDING", "NULL", 30)
	oldest := insertOutboxRow(t, pool, "PENDING", "NULL", 10)
	middle := insertOutboxRow(t, pool, "PENDING", "NULL", 20)

	claimed, err := repo.ClaimPending(context.Background(), 2, 60*time.Second)
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	if len(claimed) != 2 || claimed[0].ID != oldest || claimed[1].ID != middle {
		t.Fatalf("expected [oldest, middle], got %d rows", len(claimed))
	}
	if r := readOutboxRow(t, pool, newest); r.dueInFuture {
		t.Fatalf("row beyond the limit must not be leased, got %+v", r)
	}
}

func TestMarkFailed_Integration_FailsPendingRowOnly(t *testing.T) {
	repo, pool := newClaimIntegrationRepo(t)
	pending := insertOutboxRow(t, pool, "PENDING", "NOW() + interval '1 minute'", 0)
	processed := insertOutboxRow(t, pool, "PROCESSED", "NULL", 1)

	if err := repo.MarkFailed(context.Background(), pending); err != nil {
		t.Fatalf("MarkFailed failed: %v", err)
	}
	if err := repo.MarkFailed(context.Background(), processed); err != nil {
		t.Fatalf("MarkFailed on processed row failed: %v", err)
	}

	if r := readOutboxRow(t, pool, pending); r.status != "FAILED" || r.retryCount != 1 || !r.dueIsNull {
		t.Fatalf("expected FAILED with retry_count 1 and NULL next_retry_at, got %+v", r)
	}
	if r := readOutboxRow(t, pool, processed); r.status != "PROCESSED" || r.retryCount != 0 {
		t.Fatalf("PROCESSED row must be unchanged, got %+v", r)
	}

	claimed, err := repo.ClaimPending(context.Background(), 100, 60*time.Second)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("FAILED/PROCESSED rows must never be claimed, got %d rows err=%v", len(claimed), err)
	}
}

func TestMarkRetry_Integration_OverridesLeaseAfterClaim(t *testing.T) {
	repo, pool := newClaimIntegrationRepo(t)
	id := insertOutboxRow(t, pool, "PENDING", "NULL", 0)

	if _, err := repo.ClaimPending(context.Background(), 100, 600*time.Second); err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	// Retry far in the past so the row is due again immediately. 48h is used on purpose: MarkRetry
	// writes the app's wall clock into a TIMESTAMP (no tz) column, so a smaller offset would be
	// swamped by any app/DB timezone difference (a known pre-existing issue, see UPLOAD_FLOW.md).
	if err := repo.MarkRetry(context.Background(), id, time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatalf("MarkRetry failed: %v", err)
	}

	r := readOutboxRow(t, pool, id)
	if r.status != "PENDING" || r.retryCount != 1 || r.dueInFuture {
		t.Fatalf("expected PENDING, retry_count 1, no longer leased, got %+v", r)
	}
	again, err := repo.ClaimPending(context.Background(), 100, 60*time.Second)
	if err != nil || len(again) != 1 || again[0].ID != id || again[0].RetryCount != 1 {
		t.Fatalf("expected the retried row to be claimable again with retry_count 1, got %d rows err=%v", len(again), err)
	}
}
