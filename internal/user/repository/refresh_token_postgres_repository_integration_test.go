package repository_test

// Integration tests for the refresh token repository against a real Postgres.
// They skip when POSTGRES_URL is absent, Postgres is unreachable, or the
// refresh_tokens table has not been created yet. Helpers (newUserIntegrationRepo,
// throwawayEmail, newTestUser, registerCleanup) come from the user repository
// integration test in this same package.
// Every test creates its own throwaway user (it-<uuid>@example.invalid); deleting
// that user in cleanup removes its tokens too (ON DELETE CASCADE).
// Connection values are never logged.

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/repository"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/service"
)

const itTTL = service.RefreshTokenTTL

// tokenFixture is a throwaway user plus the repositories and pool to work with.
type tokenFixture struct {
	repo   *repository.RefreshTokenPostgresRepository
	pool   *pgxpool.Pool
	userID string
	ctx    context.Context
}

// newTokenFixture creates a throwaway user and registers its cleanup.
func newTokenFixture(t *testing.T) *tokenFixture {
	t.Helper()
	userRepo, pool := newUserIntegrationRepo(t)

	// Skip (not fail) when the migration has not been applied.
	var exists bool
	if err := pool.QueryRow(context.Background(), `SELECT to_regclass('refresh_tokens') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		t.Skip("refresh_tokens table missing; apply the migration first")
	}

	email := throwawayEmail()
	user := newTestUser(email, "x")
	if err := userRepo.CreateUser(user); err != nil {
		t.Fatalf("create throwaway user: %v", err)
	}
	registerCleanup(t, pool, email) // deleting the user cascades to its tokens

	return &tokenFixture{
		repo:   repository.NewRefreshTokenPostgresRepository(pool),
		pool:   pool,
		userID: user.ID,
		ctx:    context.Background(),
	}
}

// newToken returns a token struct for this user with a fresh id, family and hash.
func (f *tokenFixture) newToken(family string) *repository.RefreshToken {
	if family == "" {
		family = uuid.New().String()
	}
	return &repository.RefreshToken{
		ID:        uuid.New().String(),
		UserID:    f.userID,
		FamilyID:  family,
		TokenHash: service.HashRefreshToken(uuid.New().String()),
	}
}

// insertRaw inserts a token with an explicit expiry offset (SQL interval text such as
// '-2 days'), used to build already-expired rows the repository API cannot create.
func (f *tokenFixture) insertRaw(t *testing.T, tok *repository.RefreshToken, expiresOffset string) {
	t.Helper()
	_, err := f.pool.Exec(f.ctx,
		`INSERT INTO refresh_tokens (id, user_id, family_id, token_hash, expires_at)
		 VALUES ($1,$2,$3,$4, NOW() + $5::interval)`,
		tok.ID, tok.UserID, tok.FamilyID, tok.TokenHash, expiresOffset)
	if err != nil {
		t.Fatalf("insert raw token: %v", err)
	}
}

func (f *tokenFixture) usedAtIsNull(t *testing.T, hash string) bool {
	t.Helper()
	var usedNull bool
	if err := f.pool.QueryRow(f.ctx, `SELECT used_at IS NULL FROM refresh_tokens WHERE token_hash = $1`, hash).Scan(&usedNull); err != nil {
		t.Fatalf("read used_at: %v", err)
	}
	return usedNull
}

func (f *tokenFixture) countForUser(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx, `SELECT COUNT(*) FROM refresh_tokens WHERE user_id = $1`, f.userID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestIntegrationRefreshCreateExpiryUsesDBClock(t *testing.T) {
	f := newTokenFixture(t)
	tok := f.newToken("")
	if err := f.repo.Create(f.ctx, tok, itTTL); err != nil {
		t.Fatal(err)
	}
	// Expiry should be about 7 days ahead of the database's own NOW().
	var ok bool
	err := f.pool.QueryRow(f.ctx,
		`SELECT expires_at > NOW() + INTERVAL '6 days 23 hours' AND expires_at < NOW() + INTERVAL '7 days 1 hour'
		 FROM refresh_tokens WHERE token_hash = $1`, tok.TokenHash).Scan(&ok)
	if err != nil || !ok {
		t.Fatalf("expires_at not ~7 days ahead (ok=%v err=%v)", ok, err)
	}
}

func TestIntegrationRefreshRotateClaimsOnce(t *testing.T) {
	f := newTokenFixture(t)
	first := f.newToken("")
	if err := f.repo.Create(f.ctx, first, itTTL); err != nil {
		t.Fatal(err)
	}

	second := f.newToken("ignored-family") // user/family must come from the old row
	second.UserID = "ignored-user"
	old, err := f.repo.Rotate(f.ctx, first.TokenHash, second, itTTL)
	if err != nil || old == nil {
		t.Fatalf("first rotate: old=%v err=%v", old, err)
	}
	if old.UserID != f.userID || old.FamilyID != first.FamilyID {
		t.Fatalf("claimed row has wrong user/family: %+v", old)
	}
	if f.usedAtIsNull(t, first.TokenHash) {
		t.Fatal("old token should be marked used")
	}

	// New token was stored in the same family for the same user.
	fam, err := f.repo.FamilyOf(f.ctx, second.TokenHash)
	if err != nil || fam != first.FamilyID {
		t.Fatalf("new token family = %q err=%v", fam, err)
	}

	// Presenting the old token again must not claim anything and must add no row.
	again, err := f.repo.Rotate(f.ctx, first.TokenHash, f.newToken(""), itTTL)
	if err != nil || again != nil {
		t.Fatalf("second rotate of same token: %v, %v", again, err)
	}
	if n := f.countForUser(t); n != 2 {
		t.Fatalf("expected 2 rows, got %d", n)
	}
}

func TestIntegrationRefreshRotateUnknownToken(t *testing.T) {
	f := newTokenFixture(t)
	old, err := f.repo.Rotate(f.ctx, service.HashRefreshToken("never-issued"), f.newToken(""), itTTL)
	if err != nil || old != nil {
		t.Fatalf("got %v, %v", old, err)
	}
	if n := f.countForUser(t); n != 0 {
		t.Fatalf("no row should be inserted, got %d", n)
	}
}

func TestIntegrationRefreshConcurrentRotateOnlyOneWins(t *testing.T) {
	f := newTokenFixture(t)
	first := f.newToken("")
	if err := f.repo.Create(f.ctx, first, itTTL); err != nil {
		t.Fatal(err)
	}

	const workers = 20
	var winners, losers, failures int32
	var wg sync.WaitGroup
	start := make(chan struct{}) // release all goroutines at the same moment
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			old, err := f.repo.Rotate(f.ctx, first.TokenHash, f.newToken(""), itTTL)
			switch {
			case err != nil:
				atomic.AddInt32(&failures, 1)
			case old != nil:
				atomic.AddInt32(&winners, 1)
			default:
				atomic.AddInt32(&losers, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if winners != 1 || losers != workers-1 || failures != 0 {
		t.Fatalf("winners=%d losers=%d failures=%d", winners, losers, failures)
	}
	// Exactly one new token exists besides the original: the losers inserted nothing.
	if n := f.countForUser(t); n != 2 {
		t.Fatalf("expected 2 rows, got %d", n)
	}
}

func TestIntegrationRefreshExpiredTokenNotRotatable(t *testing.T) {
	f := newTokenFixture(t)
	expired := f.newToken("")
	f.insertRaw(t, expired, "-1 hour")

	old, err := f.repo.Rotate(f.ctx, expired.TokenHash, f.newToken(""), itTTL)
	if err != nil || old != nil {
		t.Fatalf("expired token must not rotate: %v, %v", old, err)
	}
	if !f.usedAtIsNull(t, expired.TokenHash) || f.countForUser(t) != 1 {
		t.Fatal("expired token must stay unused and nothing may be inserted")
	}
}

func TestIntegrationRefreshRevokedFamilyBlocksAllTokens(t *testing.T) {
	f := newTokenFixture(t)
	a := f.newToken("")
	if err := f.repo.Create(f.ctx, a, itTTL); err != nil {
		t.Fatal(err)
	}
	b := f.newToken("")
	if _, err := f.repo.Rotate(f.ctx, a.TokenHash, b, itTTL); err != nil {
		t.Fatal(err)
	}
	// A second, still-unused token in the same family (e.g. issued on another rotation).
	c := f.newToken(a.FamilyID)
	if err := f.repo.Create(f.ctx, c, itTTL); err != nil {
		t.Fatal(err)
	}
	// A token of a DIFFERENT family must be unaffected.
	other := f.newToken("")
	if err := f.repo.Create(f.ctx, other, itTTL); err != nil {
		t.Fatal(err)
	}

	if err := f.repo.RevokeFamily(f.ctx, a.FamilyID); err != nil {
		t.Fatal(err)
	}

	for name, hash := range map[string]string{"b": b.TokenHash, "c": c.TokenHash} {
		old, err := f.repo.Rotate(f.ctx, hash, f.newToken(""), itTTL)
		if err != nil || old != nil {
			t.Fatalf("revoked token %s must not rotate: %v, %v", name, old, err)
		}
	}
	// Revoking twice is harmless.
	if err := f.repo.RevokeFamily(f.ctx, a.FamilyID); err != nil {
		t.Fatal(err)
	}
	if old, err := f.repo.Rotate(f.ctx, other.TokenHash, f.newToken(""), itTTL); err != nil || old == nil {
		t.Fatalf("other family must still work: %v, %v", old, err)
	}
}

func TestIntegrationRefreshRotateRollsBackWhenInsertFails(t *testing.T) {
	f := newTokenFixture(t)
	a := f.newToken("")
	b := f.newToken("")
	if err := f.repo.Create(f.ctx, a, itTTL); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Create(f.ctx, b, itTTL); err != nil {
		t.Fatal(err)
	}

	// The new token reuses b's hash, so the INSERT violates the UNIQUE constraint.
	dup := &repository.RefreshToken{ID: uuid.New().String(), TokenHash: b.TokenHash}
	old, err := f.repo.Rotate(f.ctx, a.TokenHash, dup, itTTL)
	if err == nil || old != nil {
		t.Fatalf("expected an insert error, got old=%v err=%v", old, err)
	}
	// The claim must have been rolled back: the old token is still unused and usable.
	if !f.usedAtIsNull(t, a.TokenHash) {
		t.Fatal("old token was burned even though the insert failed")
	}
	if got, err := f.repo.Rotate(f.ctx, a.TokenHash, f.newToken(""), itTTL); err != nil || got == nil {
		t.Fatalf("old token should still rotate: %v, %v", got, err)
	}
}

func TestIntegrationRefreshFamilyOf(t *testing.T) {
	f := newTokenFixture(t)
	a := f.newToken("")
	f.insertRaw(t, a, "-3 days") // expired tokens still report their family
	if fam, err := f.repo.FamilyOf(f.ctx, a.TokenHash); err != nil || fam != a.FamilyID {
		t.Fatalf("family = %q err=%v", fam, err)
	}
	if fam, err := f.repo.FamilyOf(f.ctx, service.HashRefreshToken("unknown")); err != nil || fam != "" {
		t.Fatalf("unknown token: %q, %v", fam, err)
	}
}

func TestIntegrationRefreshDeleteExpiredKeepsRecent(t *testing.T) {
	f := newTokenFixture(t)
	longExpired := f.newToken("") // expired 2 days ago: deleted
	recent := f.newToken("")      // expired 12 hours ago: inside the 1-day grace, kept
	live := f.newToken("")        // still valid: kept
	f.insertRaw(t, longExpired, "-2 days")
	f.insertRaw(t, recent, "-12 hours")
	f.insertRaw(t, live, "1 day")

	// Another user's long-expired row must not be touched.
	userRepo, _ := newUserIntegrationRepo(t)
	otherEmail := throwawayEmail()
	otherUser := newTestUser(otherEmail, "x")
	if err := userRepo.CreateUser(otherUser); err != nil {
		t.Fatal(err)
	}
	registerCleanup(t, f.pool, otherEmail)
	otherTok := f.newToken("")
	otherTok.UserID = otherUser.ID
	f.insertRaw(t, otherTok, "-5 days")

	if err := f.repo.DeleteExpiredForUser(f.ctx, f.userID); err != nil {
		t.Fatal(err)
	}

	exists := func(hash string) bool {
		var n int
		_ = f.pool.QueryRow(f.ctx, `SELECT COUNT(*) FROM refresh_tokens WHERE token_hash = $1`, hash).Scan(&n)
		return n == 1
	}
	if exists(longExpired.TokenHash) {
		t.Fatal("token expired 2 days ago should be deleted")
	}
	if !exists(recent.TokenHash) || !exists(live.TokenHash) {
		t.Fatal("recently expired and live tokens must be kept")
	}
	if !exists(otherTok.TokenHash) {
		t.Fatal("another user's tokens must not be deleted")
	}
}

func TestIntegrationRefreshOnlyHashIsStored(t *testing.T) {
	f := newTokenFixture(t)
	raw, hash, err := service.GenerateRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	tok := &repository.RefreshToken{ID: uuid.New().String(), UserID: f.userID, FamilyID: uuid.New().String(), TokenHash: hash}
	if err := f.repo.Create(f.ctx, tok, itTTL); err != nil {
		t.Fatal(err)
	}

	// Render the whole row as text and make sure the raw token is nowhere in it.
	var rowText string
	if err := f.pool.QueryRow(f.ctx, `SELECT t::text FROM refresh_tokens t WHERE token_hash = $1`, hash).Scan(&rowText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rowText, raw) {
		t.Fatal("raw refresh token found in the stored row")
	}
	if !strings.Contains(rowText, hash) || len(hash) != 64 {
		t.Fatal("hash not stored as 64-char SHA-256 hex")
	}
}

func TestIntegrationRefreshCascadeAndNoLeftovers(t *testing.T) {
	f := newTokenFixture(t)
	if err := f.repo.Create(f.ctx, f.newToken(""), itTTL); err != nil {
		t.Fatal(err)
	}
	// Deleting the user must remove its tokens (ON DELETE CASCADE).
	if _, err := f.pool.Exec(f.ctx, `DELETE FROM users WHERE id = $1`, f.userID); err != nil {
		t.Fatal(err)
	}
	if n := f.countForUser(t); n != 0 {
		t.Fatalf("%d token rows left after deleting the user", n)
	}
}

func TestIntegrationRefreshNoLeftoverThrowawayRows(t *testing.T) {
	// Runs after the other tests in this file (Go runs tests in source order); any
	// throwaway user left behind would still own tokens through the foreign key.
	_, pool := newUserIntegrationRepo(t)
	var users, tokens int
	_ = pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM users WHERE email LIKE 'it-%@example.invalid'`).Scan(&users)
	_ = pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM refresh_tokens r JOIN users u ON u.id = r.user_id WHERE u.email LIKE 'it-%@example.invalid'`).Scan(&tokens)
	if users != 0 || tokens != 0 {
		t.Fatalf("leftover throwaway rows: users=%d tokens=%d", users, tokens)
	}
}
