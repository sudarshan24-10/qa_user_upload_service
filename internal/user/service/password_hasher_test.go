package service

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// newTestHasher uses bcrypt.MinCost so the tests run in milliseconds.
func newTestHasher() *bcryptHasher {
	return NewBcryptHasher(bcrypt.MinCost)
}

func TestHashDoesNotContainPasswordAndLooksLikeBcrypt(t *testing.T) {
	password := "correct-horse-battery"
	hash, err := newTestHasher().Hash(password)
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if strings.Contains(hash, password) {
		t.Error("hash contains the plaintext password")
	}
	if !strings.HasPrefix(hash, "$2") {
		t.Errorf("hash %q does not start with $2", hash)
	}
}

func TestRoundTripAndWrongPassword(t *testing.T) {
	h := newTestHasher()
	hash, err := h.Hash("s3cret-password")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if err := h.Compare(hash, "s3cret-password"); err != nil {
		t.Errorf("Compare() with correct password error = %v", err)
	}
	err = h.Compare(hash, "wrong-password")
	if !errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		t.Errorf("Compare() with wrong password error = %v, want ErrMismatchedHashAndPassword", err)
	}
}

// A random salt means the same password never produces the same hash twice.
func TestSamePasswordGivesDifferentHashes(t *testing.T) {
	h := newTestHasher()
	a, _ := h.Hash("same-password")
	b, _ := h.Hash("same-password")
	if a == b {
		t.Error("two hashes of the same password are identical; salt is not random")
	}
}

func TestHashEmbedsConfiguredCost(t *testing.T) {
	hash, err := NewBcryptHasher(bcrypt.MinCost + 1).Hash("password")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatalf("bcrypt.Cost() error = %v", err)
	}
	if cost != bcrypt.MinCost+1 {
		t.Errorf("cost = %d, want %d", cost, bcrypt.MinCost+1)
	}
}

// A stored value that is not a bcrypt hash (e.g. plaintext) must fail.
func TestCompareWithNonBcryptHashFails(t *testing.T) {
	if err := newTestHasher().Compare("plain-text-password", "plain-text-password"); err == nil {
		t.Error("Compare() against a non-bcrypt value returned nil, want error")
	}
}

func TestEmptyPassword(t *testing.T) {
	h := newTestHasher()
	hash, err := h.Hash("")
	if err != nil {
		t.Fatalf("Hash(\"\") error = %v", err)
	}
	if err := h.Compare(hash, ""); err != nil {
		t.Errorf("Compare() empty vs empty error = %v", err)
	}
	if err := h.Compare(hash, "not-empty"); err == nil {
		t.Error("Compare() empty hash vs non-empty password returned nil")
	}
}

// Hash with an out-of-range cost surfaces bcrypt's error unchanged-in-effect.
func TestHashInvalidCostIsAccepted(t *testing.T) {
	// bcrypt silently raises a too-low cost to the default, so this succeeds.
	if _, err := NewBcryptHasher(1).Hash("password"); err != nil {
		t.Errorf("Hash() with low cost error = %v", err)
	}
	if _, err := NewBcryptHasher(100).Hash("password"); err == nil {
		t.Error("Hash() with cost above max returned nil, want error")
	}
}

// Behaviour of x/crypto v0.46.0: Hash rejects >72 bytes with ErrPasswordTooLong.
func TestPasswordLongerThan72Bytes(t *testing.T) {
	h := newTestHasher()
	long := strings.Repeat("a", maxPasswordBytes+1)
	if _, err := h.Hash(long); !errors.Is(err, bcrypt.ErrPasswordTooLong) {
		t.Errorf("Hash(73 bytes) error = %v, want ErrPasswordTooLong", err)
	}
	// Exactly 72 bytes is allowed.
	exact := strings.Repeat("a", maxPasswordBytes)
	if _, err := h.Hash(exact); err != nil {
		t.Errorf("Hash(72 bytes) error = %v", err)
	}
}
