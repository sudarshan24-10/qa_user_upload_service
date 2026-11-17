package service

// Tests for the token side of the user service: login issuing tokens, refresh
// rotation, reuse detection and logout. They use an in-memory refresh repository and
// a fake clock (see user_service_test.go), so no database and no sleeping.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/dto"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/model"
)

var bg = context.Background()

// tokenSetup registers a@x.com and returns the service plus its fakes.
func tokenSetup(t *testing.T) (*userService, *fakeUserRepo, *fakeRefreshRepo, *fakeClock) {
	t.Helper()
	repo := newFakeUserRepo()
	svc, _, refresh, clk := newTestServiceFull(repo)
	if err := svc.RegisterUser(registerReq("a@x.com", "password123")); err != nil {
		t.Fatal(err)
	}
	return svc, repo, refresh, clk
}

func isInvalidRefresh(err error) bool {
	var target *model.InvalidRefreshTokenError
	return errors.As(err, &target)
}

// ---------- login ----------

func TestLoginReturnsTokensAndStoresOnlyHash(t *testing.T) {
	svc, repo, refresh, clk := tokenSetup(t)
	tok := login(t, svc, "a@x.com", "password123")

	if tok.ExpiresIn != 900 {
		t.Errorf("ExpiresIn = %d, want 900", tok.ExpiresIn)
	}
	// The access token is a real JWT whose subject is the user's id.
	sub, err := svc.tokens.ParseAccessToken(tok.AccessToken)
	if err != nil || sub != repo.users["a@x.com"].ID {
		t.Errorf("access token subject = %q, err = %v", sub, err)
	}
	// The refresh row is stored under the HASH; the raw token is nowhere in the store.
	row, ok := refresh.tokens[HashRefreshToken(tok.RefreshToken)]
	if !ok {
		t.Fatal("refresh token hash was not stored")
	}
	if _, rawStored := refresh.tokens[tok.RefreshToken]; rawStored {
		t.Error("raw refresh token was used as a key")
	}
	if strings.Contains(row.TokenHash, tok.RefreshToken) || row.TokenHash == tok.RefreshToken {
		t.Error("raw refresh token stored")
	}
	if row.UserID != sub || row.FamilyID == "" {
		t.Errorf("row = %+v", row)
	}
	// Refresh token lifetime is 7 days.
	if !row.expiresAt.Equal(clk.now().Add(7 * 24 * time.Hour)) {
		t.Errorf("expiresAt = %v", row.expiresAt)
	}
}

func TestLoginEachSessionGetsItsOwnFamily(t *testing.T) {
	svc, _, refresh, _ := tokenSetup(t)
	a := login(t, svc, "a@x.com", "password123")
	b := login(t, svc, "a@x.com", "password123")
	fa := refresh.tokens[HashRefreshToken(a.RefreshToken)].FamilyID
	fb := refresh.tokens[HashRefreshToken(b.RefreshToken)].FamilyID
	if fa == fb {
		t.Error("two logins must not share a family")
	}
}

func TestLoginRunsCleanupForThatUser(t *testing.T) {
	svc, repo, refresh, _ := tokenSetup(t)
	login(t, svc, "a@x.com", "password123")
	if len(refresh.cleanupCalls) != 1 || refresh.cleanupCalls[0] != repo.users["a@x.com"].ID {
		t.Errorf("cleanup calls = %v", refresh.cleanupCalls)
	}
}

func TestLoginCleanupFailureStillSucceeds(t *testing.T) {
	svc, _, refresh, _ := tokenSetup(t)
	refresh.cleanupErr = errors.New("cleanup boom")
	if tok := login(t, svc, "a@x.com", "password123"); tok.RefreshToken == "" {
		t.Error("login should still return tokens")
	}
}

func TestLoginWrongPasswordCreatesNoToken(t *testing.T) {
	svc, _, refresh, _ := tokenSetup(t)
	_, err := svc.LoginUser(bg, dto.LoginUserRequest{Email: "a@x.com", Password: "wrong-pass"})
	if err == nil || refresh.createCalls != 0 || len(refresh.cleanupCalls) != 0 {
		t.Errorf("err=%v createCalls=%d cleanup=%v", err, refresh.createCalls, refresh.cleanupCalls)
	}
}

func TestLoginStoreFailureIsReturnedNotInvalidCredentials(t *testing.T) {
	svc, _, refresh, _ := tokenSetup(t)
	refresh.createErr = errors.New("db down")
	_, err := svc.LoginUser(bg, dto.LoginUserRequest{Email: "a@x.com", Password: "password123"})
	var creds *model.InvalidCredentialsError
	if err == nil || errors.As(err, &creds) || !errors.Is(err, refresh.createErr) {
		t.Errorf("err = %v, want wrapped store error", err)
	}
}

// ---------- refresh ----------

func TestRefreshRotates(t *testing.T) {
	svc, repo, refresh, clk := tokenSetup(t)
	first := login(t, svc, "a@x.com", "password123")
	clk.advance(5 * time.Minute)

	next, err := svc.RefreshToken(bg, first.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if next.RefreshToken == first.RefreshToken || next.AccessToken == first.AccessToken {
		t.Error("refresh must return NEW tokens")
	}
	if next.ExpiresIn != 900 {
		t.Errorf("ExpiresIn = %d", next.ExpiresIn)
	}
	if sub, err := svc.tokens.ParseAccessToken(next.AccessToken); err != nil || sub != repo.users["a@x.com"].ID {
		t.Errorf("new access token subject = %q, err = %v", sub, err)
	}
	oldRow := refresh.tokens[HashRefreshToken(first.RefreshToken)]
	newRow := refresh.tokens[HashRefreshToken(next.RefreshToken)]
	if !oldRow.used {
		t.Error("old token should be marked used")
	}
	if newRow == nil || newRow.FamilyID != oldRow.FamilyID || newRow.UserID != oldRow.UserID {
		t.Errorf("new row must be in the same family and user: %+v", newRow)
	}
	// The new token can itself be rotated (a chain of refreshes works).
	if _, err := svc.RefreshToken(bg, next.RefreshToken); err != nil {
		t.Errorf("second rotation failed: %v", err)
	}
}

// Example: R1 is rotated into R2; a thief presents R1 again. R2 must die too.
func TestRefreshReuseRevokesFamily(t *testing.T) {
	svc, _, refresh, _ := tokenSetup(t)
	r1 := login(t, svc, "a@x.com", "password123")
	r2, err := svc.RefreshToken(bg, r1.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.RefreshToken(bg, r1.RefreshToken); !isInvalidRefresh(err) {
		t.Fatalf("reused token: err = %v, want InvalidRefreshTokenError", err)
	}
	if len(refresh.revokeCalls) != 1 {
		t.Fatalf("revoke calls = %v", refresh.revokeCalls)
	}
	// R2 was never reused, yet it is now dead because its family was revoked.
	if _, err := svc.RefreshToken(bg, r2.RefreshToken); !isInvalidRefresh(err) {
		t.Errorf("R2 after reuse: err = %v, want InvalidRefreshTokenError", err)
	}
}

func TestRefreshOtherSessionUnaffectedByReuse(t *testing.T) {
	svc, _, _, _ := tokenSetup(t)
	a := login(t, svc, "a@x.com", "password123")
	b := login(t, svc, "a@x.com", "password123") // a second device
	_, _ = svc.RefreshToken(bg, a.RefreshToken)
	_, _ = svc.RefreshToken(bg, a.RefreshToken) // reuse: revokes only a's family
	if _, err := svc.RefreshToken(bg, b.RefreshToken); err != nil {
		t.Errorf("other session should keep working: %v", err)
	}
}

func TestRefreshUnknownTokenFailsWithoutRevoke(t *testing.T) {
	svc, _, refresh, _ := tokenSetup(t)
	for _, raw := range []string{"never-issued", ""} {
		if _, err := svc.RefreshToken(bg, raw); !isInvalidRefresh(err) {
			t.Errorf("%q: err = %v", raw, err)
		}
	}
	if len(refresh.revokeCalls) != 0 {
		t.Errorf("unknown tokens must not revoke anything: %v", refresh.revokeCalls)
	}
}

func TestRefreshExpiredTokenFails(t *testing.T) {
	svc, _, _, clk := tokenSetup(t)
	r1 := login(t, svc, "a@x.com", "password123")
	clk.advance(7*24*time.Hour + time.Second)
	if _, err := svc.RefreshToken(bg, r1.RefreshToken); !isInvalidRefresh(err) {
		t.Errorf("err = %v, want InvalidRefreshTokenError", err)
	}
}

func TestRefreshJustBeforeExpirySucceeds(t *testing.T) {
	svc, _, _, clk := tokenSetup(t)
	r1 := login(t, svc, "a@x.com", "password123")
	clk.advance(7*24*time.Hour - time.Second)
	if _, err := svc.RefreshToken(bg, r1.RefreshToken); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestRefreshInfrastructureErrorsAreNot401(t *testing.T) {
	t.Run("rotate fails", func(t *testing.T) {
		svc, _, refresh, _ := tokenSetup(t)
		refresh.rotateErr = errors.New("db down")
		_, err := svc.RefreshToken(bg, "x")
		if isInvalidRefresh(err) || !errors.Is(err, refresh.rotateErr) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("family lookup fails", func(t *testing.T) {
		svc, _, refresh, _ := tokenSetup(t)
		refresh.familyErr = errors.New("db down")
		_, err := svc.RefreshToken(bg, "x")
		if isInvalidRefresh(err) || !errors.Is(err, refresh.familyErr) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("revoke fails", func(t *testing.T) {
		svc, _, refresh, _ := tokenSetup(t)
		r1 := login(t, svc, "a@x.com", "password123")
		_, _ = svc.RefreshToken(bg, r1.RefreshToken)
		refresh.revokeErr = errors.New("db down")
		_, err := svc.RefreshToken(bg, r1.RefreshToken) // reuse, but revoke fails
		if isInvalidRefresh(err) || !errors.Is(err, refresh.revokeErr) {
			t.Errorf("err = %v", err)
		}
	})
}

// ---------- logout ----------

func TestLogoutRevokesWholeFamily(t *testing.T) {
	svc, _, _, _ := tokenSetup(t)
	r1 := login(t, svc, "a@x.com", "password123")
	r2, _ := svc.RefreshToken(bg, r1.RefreshToken)

	if err := svc.Logout(bg, r2.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RefreshToken(bg, r2.RefreshToken); !isInvalidRefresh(err) {
		t.Errorf("refresh after logout: err = %v", err)
	}
}

func TestLogoutUnknownTokenIsSuccess(t *testing.T) {
	svc, _, refresh, _ := tokenSetup(t)
	if err := svc.Logout(bg, "never-issued"); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if len(refresh.revokeCalls) != 0 {
		t.Error("nothing should be revoked for an unknown token")
	}
}

func TestLogoutTwiceIsSuccess(t *testing.T) {
	svc, _, _, _ := tokenSetup(t)
	r1 := login(t, svc, "a@x.com", "password123")
	for i := 0; i < 2; i++ {
		if err := svc.Logout(bg, r1.RefreshToken); err != nil {
			t.Errorf("logout #%d: %v", i+1, err)
		}
	}
}

func TestLogoutInfrastructureErrorsAreReturned(t *testing.T) {
	svc, _, refresh, _ := tokenSetup(t)
	r1 := login(t, svc, "a@x.com", "password123")

	refresh.familyErr = errors.New("db down")
	if err := svc.Logout(bg, r1.RefreshToken); !errors.Is(err, refresh.familyErr) {
		t.Errorf("family lookup error not returned: %v", err)
	}
	refresh.familyErr = nil
	refresh.revokeErr = errors.New("db down")
	if err := svc.Logout(bg, r1.RefreshToken); !errors.Is(err, refresh.revokeErr) {
		t.Errorf("revoke error not returned: %v", err)
	}
}
