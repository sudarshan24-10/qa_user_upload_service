package model

import (
	"errors"
	"fmt"
	"testing"
)

// Each Error() text is part of the API contract (it is sent to clients), so
// the exact strings are asserted here.
func TestUserErrorMessages(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"email exists", &EmailAlreadyExistsError{}, "an account with this email already exists"},
		{"invalid credentials", &InvalidCredentialsError{}, "invalid email or password"},
		{"invalid password", &InvalidPasswordError{Reason: "password must be at most 72 bytes"}, "password must be at most 72 bytes"},
		{"invalid refresh token", &InvalidRefreshTokenError{}, "invalid or expired refresh token"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

// InvalidPasswordError must echo whatever Reason the service sets, so a
// different rule produces a different message.
func TestInvalidPasswordErrorReturnsReason(t *testing.T) {
	err := &InvalidPasswordError{Reason: "password must be at least 8 characters"}
	if got := err.Error(); got != err.Reason {
		t.Errorf("Error() = %q, want Reason %q", got, err.Reason)
	}
}

// Services wrap errors with fmt.Errorf("%w"); the handler must still be able
// to find the typed error with errors.As.
func TestUserErrorsAsThroughWrapping(t *testing.T) {
	t.Run("email exists", func(t *testing.T) {
		wrapped := fmt.Errorf("signup: %w", &EmailAlreadyExistsError{})
		var target *EmailAlreadyExistsError
		if !errors.As(wrapped, &target) {
			t.Error("errors.As did not find EmailAlreadyExistsError")
		}
	})

	t.Run("invalid credentials", func(t *testing.T) {
		wrapped := fmt.Errorf("login: %w", &InvalidCredentialsError{})
		var target *InvalidCredentialsError
		if !errors.As(wrapped, &target) {
			t.Error("errors.As did not find InvalidCredentialsError")
		}
	})

	t.Run("invalid password keeps reason", func(t *testing.T) {
		wrapped := fmt.Errorf("signup: %w", &InvalidPasswordError{Reason: "too short"})
		var target *InvalidPasswordError
		if !errors.As(wrapped, &target) {
			t.Fatal("errors.As did not find InvalidPasswordError")
		}
		if target.Reason != "too short" {
			t.Errorf("Reason = %q, want %q", target.Reason, "too short")
		}
	})

	// A different typed error must not match, otherwise the handler could
	// return the wrong HTTP status.
	t.Run("no cross matching", func(t *testing.T) {
		wrapped := fmt.Errorf("login: %w", &InvalidCredentialsError{})
		var target *EmailAlreadyExistsError
		if errors.As(wrapped, &target) {
			t.Error("errors.As matched the wrong error type")
		}
	})
}
