package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Token settings for v1. Hard-coded on purpose (same approach as the rate limiter).
const (
	// AccessTokenTTL is how long an access token (JWT) is valid.
	AccessTokenTTL = 15 * time.Minute
	// RefreshTokenTTL is how long a refresh token is valid.
	RefreshTokenTTL = 7 * 24 * time.Hour
	// AccessTokenIssuer is the "iss" claim. The parser requires it, so a JWT signed
	// with the same secret by some other service is not accepted here.
	AccessTokenIssuer = "qa_user_upload_service"

	// refreshTokenBytes is the size of the random refresh token: 32 bytes = 256 bits.
	refreshTokenBytes = 32
)

// ErrInvalidAccessToken is returned for every kind of bad access token (malformed,
// wrong signature, wrong algorithm, expired, wrong issuer, ...). One error on
// purpose so callers cannot tell the cases apart.
var ErrInvalidAccessToken = errors.New("invalid access token")

// Clock returns "now". It is injected so tests can control time (expiry checks)
// without sleeping.
type Clock func() time.Time

// TokenService creates and checks access tokens (JWT, HS256).
type TokenService struct {
	secret    []byte
	accessTTL time.Duration
	issuer    string
	now       Clock
}

// NewTokenService builds a TokenService. The secret length is checked once at
// startup in config.Load, so it is not re-checked here. A nil clock means time.Now.
func NewTokenService(secret string, accessTTL time.Duration, issuer string, now Clock) *TokenService {
	if now == nil {
		now = time.Now
	}
	return &TokenService{secret: []byte(secret), accessTTL: accessTTL, issuer: issuer, now: now}
}

// IssueAccessToken signs a JWT for the user: sub = user id, iss, iat = now,
// exp = now + accessTTL. The token is NOT stored anywhere and cannot be revoked
// before it expires (by design, see the 2B plan).
func (t *TokenService) IssueAccessToken(userID string) (string, error) {
	now := t.now()
	claims := jwt.RegisteredClaims{
		Subject:   userID,
		Issuer:    t.issuer,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(t.accessTTL)),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
}

// ParseAccessToken verifies a JWT and returns the user id (the "sub" claim).
// Why each option:
//   - WithValidMethods(HS256): only HS256 is accepted, so "alg: none" and every
//     other algorithm (e.g. HS512, RS256) are rejected.
//   - WithExpirationRequired: a token without "exp" is rejected, not treated as eternal.
//   - WithIssuer: "iss" must match.
//   - WithTimeFunc: expiry is judged by the injected clock.
//
// Any failure returns ErrInvalidAccessToken.
func (t *TokenService) ParseAccessToken(raw string) (string, error) {
	claims := &jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(
		raw,
		claims,
		func(*jwt.Token) (any, error) { return t.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithIssuer(t.issuer),
		jwt.WithTimeFunc(t.now),
	)
	if err != nil || claims.Subject == "" {
		return "", ErrInvalidAccessToken
	}
	return claims.Subject, nil
}

// GenerateRefreshToken creates a random opaque refresh token. It returns the raw
// value (sent to the client once, never stored) and its hash (what the database
// stores). 256 random bits from crypto/rand, base64url without padding (43 chars).
func GenerateRefreshToken() (raw string, hash string, err error) {
	b := make([]byte, refreshTokenBytes)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, HashRefreshToken(raw), nil
}

// HashRefreshToken returns the SHA-256 of the raw token as lowercase hex. Used both
// when storing a new token and when looking up a presented one. A plain unsalted
// SHA-256 is enough because the input already has 256 bits of randomness (unlike a
// password, it cannot be guessed or brute-forced from a dictionary).
func HashRefreshToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
