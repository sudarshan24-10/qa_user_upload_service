package service

import "golang.org/x/crypto/bcrypt"

// DefaultPasswordHashCost is the bcrypt cost (work factor) used for real
// passwords. Each +1 doubles the work, and 12 takes roughly 250 ms per hash.
// That slowness is deliberate: it is barely noticeable for one login but makes
// brute-forcing a stolen hash very expensive. Exported so app.go can pass it
// to NewBcryptHasher.
const DefaultPasswordHashCost = 12

// maxPasswordBytes is the longest password bcrypt can use. bcrypt only looks at
// the first 72 bytes, so two long passwords sharing a 72-byte prefix would
// silently be treated as equal. The service rejects longer passwords instead of
// truncating them. Used by user_service.go in a later file.
const maxPasswordBytes = 72

// PasswordHasher hides how passwords are hashed and checked. It is an
// interface so the service can use real bcrypt in production and tests can
// swap in a fast or fake hasher.
type PasswordHasher interface {
	// Hash turns a plaintext password into a salted hash that is safe to store.
	Hash(password string) (string, error)
	// Compare returns nil if password matches hash, and an error otherwise.
	Compare(hash string, password string) error
}

// bcryptHasher is the production PasswordHasher, backed by bcrypt.
type bcryptHasher struct {
	cost int
}

// NewBcryptHasher builds a bcrypt hasher with the given cost
// (see DefaultPasswordHashCost). Tests can pass bcrypt.MinCost to run fast.
func NewBcryptHasher(cost int) *bcryptHasher {
	return &bcryptHasher{cost: cost}
}

// Hash returns a bcrypt hash of the password. bcrypt generates a random salt
// and stores it, together with the cost, inside the returned string, so no
// separate salt column is needed and hashing the same password twice gives
// different results. Errors are returned unchanged for the caller to map.
func (h *bcryptHasher) Hash(password string) (string, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), h.cost)
	if err != nil {
		return "", err
	}
	return string(hashed), nil
}

// Compare checks a plaintext password against a stored bcrypt hash. It runs in
// constant time with respect to the hash contents. It returns nil on a match.
// A wrong password gives bcrypt.ErrMismatchedHashAndPassword, and a stored value
// that is not a valid bcrypt hash (for example legacy plaintext) also gives an
// error. The service treats every error as "invalid credentials".
func (h *bcryptHasher) Compare(hash string, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}
