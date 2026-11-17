package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/model"
)

type UserPostgresRepository struct {
	db *pgxpool.Pool
}

func NewUserPostgresRepository(db *pgxpool.Pool) *UserPostgresRepository {
	return &UserPostgresRepository{db: db}
}

func (r *UserPostgresRepository) CreateUser(user *model.User) error {
	query := `
		INSERT INTO users (
			id, first_name, last_name, email, password_hash, phone_number,
			is_admin, is_verified, google_id, created_at, updated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		`

	_, error := r.db.Exec(context.Background(), query, user.ID, user.FirstName, user.LastName, user.Email, user.PasswordHash, user.PhoneNumber, user.IsAdmin, user.IsVerified, user.GoogleID, user.CreatedAt, user.UpdatedAt)
	if error != nil {
		// Translate a duplicate-email database error into a typed error (see helper).
		return mapCreateUserError(error)
	}
	return nil
}

// Postgres names the inline `email TEXT UNIQUE` constraint from the users migration
// "<table>_<column>_key". Only a violation of this constraint means "email taken".
const (
	pgUniqueViolationCode = "23505"
	usersEmailConstraint  = "users_email_key"
)

// mapCreateUserError turns the database error from CreateUser into a typed error.
// Why: two simultaneous signups can both pass the service's "email exists" check;
// the unique index on email is the final guard, so the loser gets a unique-violation
// from Postgres. We convert that into EmailAlreadyExistsError (HTTP 409 later)
// instead of letting it surface as a 500.
// Only the email constraint is mapped: any other unique violation (e.g. the primary
// key on id) or any non-Postgres error is returned unchanged.
func mapCreateUserError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolationCode && pgErr.ConstraintName == usersEmailConstraint {
		return &model.EmailAlreadyExistsError{}
	}
	return err
}

func (r *UserPostgresRepository) GetUserByEmail(email string) (*model.User, error) {
	query := `SELECT id, first_name, last_name, email, password_hash, phone_number, is_admin, is_verified, google_id, created_at, updated_at FROM users WHERE email = $1`

	row := r.db.QueryRow(context.Background(), query, email)

	var user model.User
	err := row.Scan(
		&user.ID,
		&user.FirstName,
		&user.LastName,
		&user.Email,
		&user.PasswordHash,
		&user.PhoneNumber,
		&user.IsAdmin,
		&user.IsVerified,
		&user.GoogleID,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return &user, nil
}
