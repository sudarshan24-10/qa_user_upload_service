package config

import (
	"strings"
	"testing"
)

// The JWT secret check is a plain function, so it is tested directly (Load itself
// calls log.Fatal and reads the environment, which a unit test cannot exercise).
func TestValidateJWTSecret(t *testing.T) {
	tests := []struct {
		name    string
		secret  string
		wantErr bool
	}{
		{"empty", "", true},
		{"31 bytes rejected", strings.Repeat("a", 31), true},
		{"32 bytes accepted", strings.Repeat("a", 32), false},
		{"longer accepted", strings.Repeat("a", 64), false},
		// 16 two-byte characters = 32 bytes: length is counted in bytes, not characters.
		{"multi-byte counts bytes", strings.Repeat("é", 16), false},
		{"15 two-byte chars = 30 bytes rejected", strings.Repeat("é", 15), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateJWTSecret(tt.secret)
			if (err != nil) != tt.wantErr {
				t.Fatalf("got err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateJWTSecretErrorDoesNotLeakSecret(t *testing.T) {
	secret := "short-secret-value"
	err := validateJWTSecret(secret)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("error message must not contain the secret")
	}
}
