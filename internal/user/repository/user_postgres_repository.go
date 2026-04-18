package repository

import (
	"context"

	"github.com/jackc/pgx/v5"
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
		return error
	}
	return nil
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
