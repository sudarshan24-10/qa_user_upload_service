package dto

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin/binding"
)

// These tests validate the `binding` tags through gin's own validator
// (binding.Validator, which reads the "binding" tag), so they check exactly
// what the HTTP handler will enforce.

func validRegister(password string, email string) ResgisterUserRequest {
	return ResgisterUserRequest{
		FirstName:   "Ada",
		LastName:    "Lovelace",
		Email:       email,
		Password:    password,
		PhoneNumber: "1234567890",
	}
}

func TestRegisterPasswordLength(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"7 chars rejected", strings.Repeat("a", 7), true},
		{"8 chars accepted", strings.Repeat("a", 8), false},
		{"72 chars accepted", strings.Repeat("a", 72), false},
		{"73 chars rejected", strings.Repeat("a", 73), true},
		{"empty rejected", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := binding.Validator.ValidateStruct(validRegister(tt.password, "a@b.com"))
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoginPasswordRules(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"short password accepted (no minimum)", "abc", false},
		{"72 chars accepted", strings.Repeat("a", 72), false},
		{"73 chars rejected", strings.Repeat("a", 73), true},
		{"empty rejected", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := binding.Validator.ValidateStruct(LoginUserRequest{Email: "a@b.com", Password: tt.password})
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// The DTO only checks email format; lowercasing is the service's job.
func TestUppercaseEmailPassesDTO(t *testing.T) {
	if err := binding.Validator.ValidateStruct(validRegister("password1", "Ada@Example.COM")); err != nil {
		t.Errorf("register: uppercase email rejected: %v", err)
	}
	if err := binding.Validator.ValidateStruct(LoginUserRequest{Email: "Ada@Example.COM", Password: "x"}); err != nil {
		t.Errorf("login: uppercase email rejected: %v", err)
	}
}
