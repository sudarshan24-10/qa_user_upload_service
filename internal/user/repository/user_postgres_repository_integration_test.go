package repository_test

// Integration tests for the user repository against a real Postgres. They skip when
// POSTGRES_URL is absent or Postgres is unreachable. This is an external test package
// (repository_test) because the register/login round trip imports the service package,
// which itself imports repository; an in-package test would be an import cycle.
// Each test creates its own throwaway users (it-<uuid>@example.invalid) and deletes
// exactly those rows by id afterwards; existing rows are never read or modified.
// Connection values are never logged.

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"golang.org/x/crypto/bcrypt"

	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/dto"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/model"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/repository"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/service"
)

func newUserIntegrationRepo(t *testing.T) (*repository.UserPostgresRepository, *pgxpool.Pool) {
	t.Helper()
	_ = godotenv.Load("../../../.env")

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

	return repository.NewUserPostgresRepository(pool), pool
}

// throwawayEmail returns a unique, clearly fake address.
func throwawayEmail() string {
	return "it-" + uuid.New().String() + "@example.invalid"
}

// registerCleanup deletes exactly the rows with these emails (our own throwaway rows).
func registerCleanup(t *testing.T, pool *pgxpool.Pool, emails ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, e := range emails {
			_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE email = $1`, e)
		}
	})
}

func newTestUser(email, hash string) *model.User {
	return &model.User{
		ID:           uuid.New().String(),
		FirstName:    "It",
		LastName:     "Test",
		Email:        email,
		PasswordHash: hash,
		PhoneNumber:  "0",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
}

func TestIntegrationDuplicateEmailReturnsTypedError(t *testing.T) {
	repo, pool := newUserIntegrationRepo(t)
	email := throwawayEmail()
	registerCleanup(t, pool, email)

	if err := repo.CreateUser(newTestUser(email, "h1")); err != nil {
		t.Fatalf("first CreateUser failed: %v", err)
	}
	err := repo.CreateUser(newTestUser(email, "h2")) // different id, same email
	var target *model.EmailAlreadyExistsError
	if !errors.As(err, &target) {
		t.Errorf("second CreateUser error = %v, want EmailAlreadyExistsError", err)
	}
}

// A duplicate primary key (same id, different email) must NOT be reported as email taken.
func TestIntegrationDuplicateIDIsNotMappedToEmailError(t *testing.T) {
	repo, pool := newUserIntegrationRepo(t)
	e1, e2 := throwawayEmail(), throwawayEmail()
	registerCleanup(t, pool, e1, e2)

	u1 := newTestUser(e1, "h")
	if err := repo.CreateUser(u1); err != nil {
		t.Fatal(err)
	}
	u2 := newTestUser(e2, "h")
	u2.ID = u1.ID
	err := repo.CreateUser(u2)
	if err == nil {
		t.Fatal("expected a primary key error")
	}
	var target *model.EmailAlreadyExistsError
	if errors.As(err, &target) {
		t.Error("primary key violation was wrongly mapped to EmailAlreadyExistsError")
	}
}

func TestIntegrationPasswordHashStoredExactly(t *testing.T) {
	repo, pool := newUserIntegrationRepo(t)
	email := throwawayEmail()
	registerCleanup(t, pool, email)
	const hash = "$2a$04$exact-hash-value-stored-as-given"

	if err := repo.CreateUser(newTestUser(email, hash)); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetUserByEmail(email)
	if err != nil || got == nil {
		t.Fatalf("GetUserByEmail = %v, %v", got, err)
	}
	if got.PasswordHash != hash {
		t.Error("stored password_hash differs from the value given")
	}
}

// Register then login through the real repository, real service and real bcrypt.
func TestIntegrationRegisterLoginRoundTrip(t *testing.T) {
	repo, pool := newUserIntegrationRepo(t)
	email := throwawayEmail() // already lowercase
	registerCleanup(t, pool, email)

	// Login now also issues a refresh token, so the real refresh repository and a
	// token service are needed. The token rows are removed with the user (cascade).
	tokens := service.NewTokenService("0123456789abcdef0123456789abcdef", service.AccessTokenTTL, service.AccessTokenIssuer, nil)
	svc := service.NewUserService(repo, service.NewBcryptHasher(bcrypt.MinCost), tokens, repository.NewRefreshTokenPostgresRepository(pool))
	ctx := context.Background()

	// Register with mixed case and spaces; the service must store it normalized.
	messy := "  " + toUpperASCII(email) + "  "
	if err := svc.RegisterUser(dto.ResgisterUserRequest{
		FirstName: "It", LastName: "Test", Email: messy, Password: "password123", PhoneNumber: "0",
	}); err != nil {
		t.Fatalf("RegisterUser failed: %v", err)
	}

	if _, err := svc.LoginUser(ctx, dto.LoginUserRequest{Email: email, Password: "password123"}); err != nil {
		t.Errorf("login with correct password failed: %v", err)
	}
	if _, err := svc.LoginUser(ctx, dto.LoginUserRequest{Email: messy, Password: "password123"}); err != nil {
		t.Errorf("login with different email case failed: %v", err)
	}
	_, err := svc.LoginUser(ctx, dto.LoginUserRequest{Email: email, Password: "wrong-password"})
	var target *model.InvalidCredentialsError
	if !errors.As(err, &target) {
		t.Errorf("wrong password error = %v, want InvalidCredentialsError", err)
	}
}

// toUpperASCII uppercases the whole address so the case differs from the stored one.
func toUpperASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}

// Confirms cleanup works: a sub-test creates a row and registers its deletion; once the
// sub-test finishes, the row must be gone.
func TestIntegrationNoLeftoverThrowawayUsers(t *testing.T) {
	_, pool := newUserIntegrationRepo(t)
	// Own sub-run: create and clean a row, then confirm it is gone.
	email := throwawayEmail()
	t.Run("create", func(t *testing.T) {
		registerCleanup(t, pool, email)
		repo := repository.NewUserPostgresRepository(pool)
		if err := repo.CreateUser(newTestUser(email, "h")); err != nil {
			t.Fatal(err)
		}
	})
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE email = $1`, email).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("throwaway row still present after cleanup: %d", n)
	}
}
