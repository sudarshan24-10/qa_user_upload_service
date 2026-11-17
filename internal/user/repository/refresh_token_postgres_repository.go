package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RefreshTokenPostgresRepository struct {
	db *pgxpool.Pool
}

func NewRefreshTokenPostgresRepository(db *pgxpool.Pool) *RefreshTokenPostgresRepository {
	return &RefreshTokenPostgresRepository{db: db}
}

// insertRefreshTokenSQL is shared by Create and Rotate. expires_at is computed by
// the database (NOW() + ttl seconds), never by the app, so there is one clock.
const insertRefreshTokenSQL = `
	INSERT INTO refresh_tokens (id, user_id, family_id, token_hash, expires_at)
	VALUES ($1, $2, $3, $4, NOW() + make_interval(secs => $5))`

func (r *RefreshTokenPostgresRepository) Create(ctx context.Context, t *RefreshToken, ttl time.Duration) error {
	_, err := r.db.Exec(ctx, insertRefreshTokenSQL, t.ID, t.UserID, t.FamilyID, t.TokenHash, ttl.Seconds())
	return err
}

func (r *RefreshTokenPostgresRepository) Rotate(ctx context.Context, oldHash string, next *RefreshToken, ttl time.Duration) (*RefreshToken, error) {
	// Everything below is one transaction: either the old token is claimed AND the
	// new one is stored, or neither happened. This avoids the crash window where the
	// old token was burned but the client never received a replacement.
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	// Rollback after a successful Commit is a harmless no-op, so this one deferred
	// call covers every early return (no row claimed, insert failure, ...).
	defer func() { _ = tx.Rollback(ctx) }()

	// Step 1: claim the old token with a single atomic UPDATE. The WHERE clause is
	// the whole validity check (unused, not revoked, not expired by the DB clock).
	// When two requests present the same token at once, Postgres lets only one of
	// them update the row; the other re-checks the WHERE, finds used_at set and gets
	// no row back. That loser is later treated as token reuse by the service.
	old := &RefreshToken{}
	err = tx.QueryRow(ctx, `
		UPDATE refresh_tokens
		SET used_at = NOW()
		WHERE token_hash = $1
		  AND used_at IS NULL
		  AND revoked_at IS NULL
		  AND expires_at > NOW()
		RETURNING id, user_id, family_id, token_hash`, oldHash,
	).Scan(&old.ID, &old.UserID, &old.FamilyID, &old.TokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // could not claim; the deferred Rollback ends the transaction
	}
	if err != nil {
		return nil, err
	}

	// Step 2: insert the next token for the SAME user and family as the old one.
	// If this fails (e.g. duplicate hash), returning here rolls back step 1 too, so
	// the old token stays usable.
	if _, err := tx.Exec(ctx, insertRefreshTokenSQL, next.ID, old.UserID, old.FamilyID, next.TokenHash, ttl.Seconds()); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return old, nil
}

func (r *RefreshTokenPostgresRepository) FamilyOf(ctx context.Context, tokenHash string) (string, error) {
	var family string
	err := r.db.QueryRow(ctx, `SELECT family_id FROM refresh_tokens WHERE token_hash = $1`, tokenHash).Scan(&family)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return family, err
}

func (r *RefreshTokenPostgresRepository) RevokeFamily(ctx context.Context, familyID string) error {
	// Only rows not yet revoked are touched, so the first revoke time is kept.
	_, err := r.db.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = NOW() WHERE family_id = $1 AND revoked_at IS NULL`, familyID)
	return err
}

func (r *RefreshTokenPostgresRepository) DeleteExpiredForUser(ctx context.Context, userID string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM refresh_tokens WHERE user_id = $1 AND expires_at < NOW() - INTERVAL '1 day'`, userID)
	return err
}
