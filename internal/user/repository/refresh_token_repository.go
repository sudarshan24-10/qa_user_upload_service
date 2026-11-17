package repository

import (
	"context"
	"time"
)

// RefreshToken is one stored refresh token row. Only the HASH of the token is ever
// stored or passed around here; the raw token exists only in the HTTP response.
type RefreshToken struct {
	ID        string // uuid of this row
	UserID    string // owner
	FamilyID  string // shared by every token of one login session
	TokenHash string // SHA-256 hex of the raw token
}

// RefreshTokenRepository stores refresh tokens. All expiry decisions use the
// database clock (NOW()), so the app clock and the DB clock can never disagree.
type RefreshTokenRepository interface {
	// Create stores a new token (used at login). It expires ttl from now (DB clock).
	Create(ctx context.Context, t *RefreshToken, ttl time.Duration) error

	// Rotate exchanges a refresh token for the next one in a single transaction:
	// it atomically claims the old token (marks it used) and inserts `next` with the
	// same user and family as the old one. next.UserID and next.FamilyID are ignored
	// and taken from the claimed row, so a caller cannot move a token between users.
	// It returns the claimed OLD token (so the caller knows user and family), or
	// (nil, nil) when the old token cannot be claimed: unknown, expired, already
	// used or revoked. If anything fails, the whole transaction is rolled back and
	// the old token stays unused.
	Rotate(ctx context.Context, oldHash string, next *RefreshToken, ttl time.Duration) (*RefreshToken, error)

	// FamilyOf returns the family of a token in ANY state (used, revoked, expired),
	// or "" if the hash is unknown. Used for reuse detection and logout.
	FamilyOf(ctx context.Context, tokenHash string) (string, error)

	// RevokeFamily marks every not-yet-revoked token of the family as revoked.
	RevokeFamily(ctx context.Context, familyID string) error

	// DeleteExpiredForUser removes the user's tokens that expired more than one day
	// ago. The one-day grace keeps recently expired rows so reuse of such a token
	// is still recognised as reuse.
	DeleteExpiredForUser(ctx context.Context, userID string) error
}
