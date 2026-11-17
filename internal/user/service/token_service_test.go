package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "0123456789abcdef0123456789abcdef" // 32 bytes, test only

// fakeClock is a controllable clock: tests move time with advance instead of sleeping.
type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time          { return f.t }
func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }

func newTestTokens(secret string) (*TokenService, *fakeClock) {
	clk := &fakeClock{t: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
	return NewTokenService(secret, AccessTokenTTL, AccessTokenIssuer, clk.now), clk
}

func TestIssueThenParseRoundTrip(t *testing.T) {
	ts, _ := newTestTokens(testSecret)
	raw, err := ts.IssueAccessToken("user-1")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ts.ParseAccessToken(raw)
	if err != nil || got != "user-1" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestIssuedClaimsAreCorrect(t *testing.T) {
	ts, clk := newTestTokens(testSecret)
	raw, _ := ts.IssueAccessToken("user-1")

	claims := jwt.MapClaims{}
	tok, _, err := jwt.NewParser().ParseUnverified(raw, claims)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Method.Alg() != "HS256" {
		t.Fatalf("alg = %s", tok.Method.Alg())
	}
	if claims["sub"] != "user-1" || claims["iss"] != AccessTokenIssuer {
		t.Fatalf("claims = %v", claims)
	}
	if int64(claims["iat"].(float64)) != clk.t.Unix() {
		t.Fatal("iat is not now")
	}
	if int64(claims["exp"].(float64)) != clk.t.Add(15*time.Minute).Unix() {
		t.Fatal("exp is not now+15m")
	}
}

func TestParseExpiredToken(t *testing.T) {
	ts, clk := newTestTokens(testSecret)
	raw, _ := ts.IssueAccessToken("user-1")
	clk.advance(15*time.Minute + time.Second)
	if _, err := ts.ParseAccessToken(raw); err != ErrInvalidAccessToken {
		t.Fatalf("err = %v", err)
	}
}

func TestParseBoundaryNotExpired(t *testing.T) {
	ts, clk := newTestTokens(testSecret)
	raw, _ := ts.IssueAccessToken("user-1")
	clk.advance(14*time.Minute + 59*time.Second)
	if _, err := ts.ParseAccessToken(raw); err != nil {
		t.Fatalf("still valid, got %v", err)
	}
}

// forge builds a token with an arbitrary header/payload and the given signing key
// and hash, to simulate attacker-made tokens.
func forge(t *testing.T, header, payload map[string]any, sign func(string) []byte) string {
	t.Helper()
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signing := enc(header) + "." + enc(payload)
	return signing + "." + base64.RawURLEncoding.EncodeToString(sign(signing))
}

func validPayload(clk *fakeClock) map[string]any {
	return map[string]any{
		"sub": "user-1", "iss": AccessTokenIssuer,
		"iat": clk.t.Unix(), "exp": clk.t.Add(time.Minute).Unix(),
	}
}

func TestParseRejectsAlgNone(t *testing.T) {
	ts, clk := newTestTokens(testSecret)
	raw := forge(t, map[string]any{"alg": "none", "typ": "JWT"}, validPayload(clk),
		func(string) []byte { return nil })
	if _, err := ts.ParseAccessToken(raw); err != ErrInvalidAccessToken {
		t.Fatalf("alg none must be rejected, got %v", err)
	}
}

func TestParseRejectsHS512(t *testing.T) {
	ts, clk := newTestTokens(testSecret)
	// Perfectly signed with the right secret, but with a different HMAC algorithm.
	tok := jwt.NewWithClaims(jwt.SigningMethodHS512, jwt.MapClaims(validPayload(clk)))
	raw, _ := tok.SignedString([]byte(testSecret))
	if _, err := ts.ParseAccessToken(raw); err != ErrInvalidAccessToken {
		t.Fatalf("HS512 must be rejected, got %v", err)
	}
}

func TestParseRejectsWrongSecret(t *testing.T) {
	ts, _ := newTestTokens(testSecret)
	other, _ := newTestTokens("ffffffffffffffffffffffffffffffff")
	raw, _ := other.IssueAccessToken("user-1")
	if _, err := ts.ParseAccessToken(raw); err != ErrInvalidAccessToken {
		t.Fatalf("err = %v", err)
	}
}

func TestParseRejectsWrongIssuer(t *testing.T) {
	ts, clk := newTestTokens(testSecret)
	p := validPayload(clk)
	p["iss"] = "someone-else"
	raw, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(p)).SignedString([]byte(testSecret))
	if _, err := ts.ParseAccessToken(raw); err != ErrInvalidAccessToken {
		t.Fatalf("err = %v", err)
	}
}

func TestParseRejectsMissingIssuer(t *testing.T) {
	ts, clk := newTestTokens(testSecret)
	p := validPayload(clk)
	delete(p, "iss")
	raw, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(p)).SignedString([]byte(testSecret))
	if _, err := ts.ParseAccessToken(raw); err != ErrInvalidAccessToken {
		t.Fatalf("err = %v", err)
	}
}

func TestParseRejectsMissingExp(t *testing.T) {
	ts, clk := newTestTokens(testSecret)
	p := validPayload(clk)
	delete(p, "exp")
	raw, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(p)).SignedString([]byte(testSecret))
	if _, err := ts.ParseAccessToken(raw); err != ErrInvalidAccessToken {
		t.Fatalf("a token with no exp must be rejected, got %v", err)
	}
}

func TestParseRejectsMissingSubject(t *testing.T) {
	ts, clk := newTestTokens(testSecret)
	p := validPayload(clk)
	delete(p, "sub")
	raw, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(p)).SignedString([]byte(testSecret))
	if _, err := ts.ParseAccessToken(raw); err != ErrInvalidAccessToken {
		t.Fatalf("err = %v", err)
	}
}

func TestParseRejectsTamperedPayload(t *testing.T) {
	ts, clk := newTestTokens(testSecret)
	raw, _ := ts.IssueAccessToken("user-1")
	parts := strings.Split(raw, ".")
	p := validPayload(clk)
	p["sub"] = "admin"
	b, _ := json.Marshal(p)
	parts[1] = base64.RawURLEncoding.EncodeToString(b) // payload changed, old signature kept
	if _, err := ts.ParseAccessToken(strings.Join(parts, ".")); err != ErrInvalidAccessToken {
		t.Fatalf("err = %v", err)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	ts, _ := newTestTokens(testSecret)
	for _, s := range []string{"", "abc", "a.b.c", "....", strings.Repeat("x", 500)} {
		if _, err := ts.ParseAccessToken(s); err != ErrInvalidAccessToken {
			t.Fatalf("%q: err = %v", s, err)
		}
	}
}

func TestNewTokenServiceNilClockUsesRealTime(t *testing.T) {
	ts := NewTokenService(testSecret, time.Minute, AccessTokenIssuer, nil)
	raw, err := ts.IssueAccessToken("user-1")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ts.ParseAccessToken(raw); err != nil || got != "user-1" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestGenerateRefreshTokenLengthAndUniqueness(t *testing.T) {
	urlSafe := regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`) // 32 bytes -> 43 base64url chars
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		raw, hash, err := GenerateRefreshToken()
		if err != nil {
			t.Fatal(err)
		}
		if !urlSafe.MatchString(raw) {
			t.Fatalf("bad token format: %q", raw)
		}
		if seen[raw] {
			t.Fatal("duplicate token")
		}
		seen[raw] = true
		if hash != HashRefreshToken(raw) {
			t.Fatal("returned hash does not match the raw token")
		}
	}
}

func TestHashIsDeterministicAndNotRaw(t *testing.T) {
	h1, h2 := HashRefreshToken("abc"), HashRefreshToken("abc")
	if h1 != h2 {
		t.Fatal("hash must be deterministic")
	}
	if h1 == "abc" || len(h1) != 64 {
		t.Fatalf("hash = %q", h1)
	}
	// Known SHA-256("abc") so the algorithm cannot silently change.
	if h1 != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatal("not SHA-256 hex")
	}
	if HashRefreshToken("abd") == h1 {
		t.Fatal("different inputs must hash differently")
	}
}

// keep hmac/sha256 imports meaningful: confirm our HS256 signature is standard HMAC-SHA256.
func TestSignatureIsStandardHS256(t *testing.T) {
	ts, _ := newTestTokens(testSecret)
	raw, _ := ts.IssueAccessToken("user-1")
	parts := strings.Split(raw, ".")
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if parts[2] != want {
		t.Fatal("signature is not HMAC-SHA256 of header.payload")
	}
}
