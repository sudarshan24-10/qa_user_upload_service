package repository

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/model"
)

// Unit tests for mapCreateUserError using hand-built pgconn.PgError values (no database).

func TestMapCreateUserError(t *testing.T) {
	emailDup := &pgconn.PgError{Code: "23505", ConstraintName: "users_email_key"}
	pkDup := &pgconn.PgError{Code: "23505", ConstraintName: "users_pkey"}
	otherCode := &pgconn.PgError{Code: "23502", ConstraintName: "users_email_key"} // not-null, not unique
	plain := errors.New("connection reset")

	t.Run("nil stays nil", func(t *testing.T) {
		if err := mapCreateUserError(nil); err != nil {
			t.Errorf("got %v, want nil", err)
		}
	})

	t.Run("email unique violation is mapped", func(t *testing.T) {
		var target *model.EmailAlreadyExistsError
		if !errors.As(mapCreateUserError(emailDup), &target) {
			t.Error("want EmailAlreadyExistsError")
		}
	})

	t.Run("wrapped email unique violation is mapped", func(t *testing.T) {
		var target *model.EmailAlreadyExistsError
		if !errors.As(mapCreateUserError(fmt.Errorf("insert: %w", emailDup)), &target) {
			t.Error("want EmailAlreadyExistsError through %w wrapping")
		}
	})

	// Every case below must come back as the very same error value.
	for name, in := range map[string]error{
		"primary key violation unchanged": pkDup,
		"different pg error unchanged":    otherCode,
		"non-pg error unchanged":          plain,
	} {
		t.Run(name, func(t *testing.T) {
			if got := mapCreateUserError(in); got != in {
				t.Errorf("got %v, want the original error unchanged", got)
			}
		})
	}
}
