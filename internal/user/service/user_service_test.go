package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/dto"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/model"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/repository"
)

// ---------- test doubles ----------

// fakeUserRepo is an in-memory UserRepository that records calls.
type fakeUserRepo struct {
	users       map[string]*model.User
	getCalls    []string // emails passed to GetUserByEmail
	createCalls int
	getErr      error
	createErr   error
	lastCreated *model.User
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{users: map[string]*model.User{}}
}

func (r *fakeUserRepo) GetUserByEmail(email string) (*model.User, error) {
	r.getCalls = append(r.getCalls, email)
	if r.getErr != nil {
		return nil, r.getErr
	}
	// Case-sensitive like the real DB index: the service must normalize.
	return r.users[email], nil
}

func (r *fakeUserRepo) CreateUser(user *model.User) error {
	r.createCalls++
	if r.createErr != nil {
		return r.createErr
	}
	r.lastCreated = user
	r.users[user.Email] = user
	return nil
}

// countingHasher wraps a real hasher and counts Compare calls.
type countingHasher struct {
	PasswordHasher
	compares int
}

func (h *countingHasher) Compare(hash string, password string) error {
	h.compares++
	return h.PasswordHasher.Compare(hash, password)
}

// failingHasher always fails.
type failingHasher struct{}

func (failingHasher) Hash(string) (string, error)  { return "", errors.New("hash boom") }
func (failingHasher) Compare(string, string) error { return errors.New("compare boom") }

// fakeRefreshRepo is an in-memory RefreshTokenRepository. It copies the rules of
// the real SQL (a token is claimable only while unused, not revoked and not expired)
// and uses the fake clock where the real one uses the database clock.
type storedToken struct {
	repository.RefreshToken
	expiresAt time.Time
	used      bool
	revoked   bool
}

type fakeRefreshRepo struct {
	clk    *fakeClock
	tokens map[string]*storedToken // keyed by token hash

	createCalls  int
	rotateCalls  int
	cleanupCalls []string // user ids passed to DeleteExpiredForUser
	revokeCalls  []string // family ids passed to RevokeFamily

	createErr, rotateErr, familyErr, revokeErr, cleanupErr error
}

func newFakeRefreshRepo(clk *fakeClock) *fakeRefreshRepo {
	return &fakeRefreshRepo{clk: clk, tokens: map[string]*storedToken{}}
}

func (r *fakeRefreshRepo) Create(_ context.Context, t *repository.RefreshToken, ttl time.Duration) error {
	r.createCalls++
	if r.createErr != nil {
		return r.createErr
	}
	r.tokens[t.TokenHash] = &storedToken{RefreshToken: *t, expiresAt: r.clk.now().Add(ttl)}
	return nil
}

func (r *fakeRefreshRepo) Rotate(_ context.Context, oldHash string, next *repository.RefreshToken, ttl time.Duration) (*repository.RefreshToken, error) {
	r.rotateCalls++
	if r.rotateErr != nil {
		return nil, r.rotateErr
	}
	old, ok := r.tokens[oldHash]
	if !ok || old.used || old.revoked || !r.clk.now().Before(old.expiresAt) {
		return nil, nil
	}
	old.used = true
	// Same user and family as the old token, like the real repository.
	r.tokens[next.TokenHash] = &storedToken{
		RefreshToken: repository.RefreshToken{ID: next.ID, UserID: old.UserID, FamilyID: old.FamilyID, TokenHash: next.TokenHash},
		expiresAt:    r.clk.now().Add(ttl),
	}
	claimed := old.RefreshToken
	return &claimed, nil
}

func (r *fakeRefreshRepo) FamilyOf(_ context.Context, hash string) (string, error) {
	if r.familyErr != nil {
		return "", r.familyErr
	}
	if t, ok := r.tokens[hash]; ok {
		return t.FamilyID, nil
	}
	return "", nil
}

func (r *fakeRefreshRepo) RevokeFamily(_ context.Context, family string) error {
	r.revokeCalls = append(r.revokeCalls, family)
	if r.revokeErr != nil {
		return r.revokeErr
	}
	for _, t := range r.tokens {
		if t.FamilyID == family {
			t.revoked = true
		}
	}
	return nil
}

func (r *fakeRefreshRepo) DeleteExpiredForUser(_ context.Context, userID string) error {
	r.cleanupCalls = append(r.cleanupCalls, userID)
	return r.cleanupErr
}

// newTestServiceFull builds the service with every fake, including the clock.
func newTestServiceFull(repo *fakeUserRepo) (*userService, *countingHasher, *fakeRefreshRepo, *fakeClock) {
	h := &countingHasher{PasswordHasher: NewBcryptHasher(bcrypt.MinCost)}
	tokens, clk := newTestTokens(testSecret)
	refresh := newFakeRefreshRepo(clk)
	return NewUserService(repo, h, tokens, refresh), h, refresh, clk
}

func newTestService(repo *fakeUserRepo) (*userService, *countingHasher) {
	svc, h, _, _ := newTestServiceFull(repo)
	return svc, h
}

// login is a short helper for tests that only need the tokens.
func login(t *testing.T, svc *userService, email, password string) *AuthTokens {
	t.Helper()
	tok, err := svc.LoginUser(context.Background(), dto.LoginUserRequest{Email: email, Password: password})
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	return tok
}

func registerReq(email, password string) dto.ResgisterUserRequest {
	return dto.ResgisterUserRequest{FirstName: "A", LastName: "B", Email: email, Password: password, PhoneNumber: "1"}
}

// ---------- RegisterUser ----------

func TestRegisterStoresHashNotPlaintext(t *testing.T) {
	repo := newFakeUserRepo()
	svc, h := newTestService(repo)
	if err := svc.RegisterUser(registerReq("a@x.com", "password123")); err != nil {
		t.Fatalf("RegisterUser() error = %v", err)
	}
	stored := repo.lastCreated.PasswordHash
	if strings.Contains(stored, "password123") {
		t.Error("plaintext password was stored")
	}
	if err := h.PasswordHasher.Compare(stored, "password123"); err != nil {
		t.Errorf("stored hash does not verify: %v", err)
	}
}

func TestRegisterStoresNormalizedEmail(t *testing.T) {
	repo := newFakeUserRepo()
	svc, _ := newTestService(repo)
	if err := svc.RegisterUser(registerReq(" Alice@X.com ", "password123")); err != nil {
		t.Fatal(err)
	}
	if got := repo.lastCreated.Email; got != "alice@x.com" {
		t.Errorf("stored email = %q, want alice@x.com", got)
	}
	if repo.getCalls[0] != "alice@x.com" {
		t.Errorf("lookup email = %q, want alice@x.com", repo.getCalls[0])
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	repo := newFakeUserRepo()
	svc, _ := newTestService(repo)
	if err := svc.RegisterUser(registerReq("a@x.com", "password123")); err != nil {
		t.Fatal(err)
	}
	err := svc.RegisterUser(registerReq("a@x.com", "password123"))
	var target *model.EmailAlreadyExistsError
	if !errors.As(err, &target) {
		t.Fatalf("error = %v, want EmailAlreadyExistsError", err)
	}
	if repo.createCalls != 1 {
		t.Errorf("CreateUser calls = %d, want 1", repo.createCalls)
	}
}

func TestRegisterDuplicateIsCaseInsensitive(t *testing.T) {
	repo := newFakeUserRepo()
	svc, _ := newTestService(repo)
	_ = svc.RegisterUser(registerReq("a@x.com", "password123"))
	err := svc.RegisterUser(registerReq("A@X.com", "password123"))
	var target *model.EmailAlreadyExistsError
	if !errors.As(err, &target) {
		t.Errorf("error = %v, want EmailAlreadyExistsError", err)
	}
}

// A unique-violation style error from CreateUser must reach the caller as-is.
func TestRegisterCreateErrorPassesThroughUnchanged(t *testing.T) {
	repo := newFakeUserRepo()
	repo.createErr = &model.EmailAlreadyExistsError{}
	svc, _ := newTestService(repo)
	err := svc.RegisterUser(registerReq("a@x.com", "password123"))
	if err != repo.createErr {
		t.Errorf("error = %v, want the exact error from CreateUser", err)
	}
}

func TestRegisterPasswordByteLimit(t *testing.T) {
	// "é" is 1 character but 2 bytes: 40 of them = 40 chars, 80 bytes.
	multiByte := strings.Repeat("é", 40)
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"73 bytes rejected", strings.Repeat("a", 73), true},
		{"multi-byte over 72 bytes rejected", multiByte, true},
		{"72 bytes accepted", strings.Repeat("a", 72), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeUserRepo()
			svc, _ := newTestService(repo)
			err := svc.RegisterUser(registerReq("a@x.com", tt.password))
			if tt.wantErr {
				var target *model.InvalidPasswordError
				if !errors.As(err, &target) {
					t.Fatalf("error = %v, want InvalidPasswordError", err)
				}
				if target.Reason != "password must be at most 72 bytes" {
					t.Errorf("Reason = %q", target.Reason)
				}
				if len(repo.getCalls) != 0 || repo.createCalls != 0 {
					t.Error("repository was called before password validation")
				}
			} else if err != nil {
				t.Errorf("error = %v, want nil", err)
			}
		})
	}
}

func TestRegisterLookupErrorIsWrapped(t *testing.T) {
	repo := newFakeUserRepo()
	repo.getErr = errors.New("db down")
	svc, _ := newTestService(repo)
	err := svc.RegisterUser(registerReq("a@x.com", "password123"))
	if err == nil || !errors.Is(err, repo.getErr) {
		t.Errorf("error = %v, want wrapped db error", err)
	}
	if repo.createCalls != 0 {
		t.Error("CreateUser was called after lookup failure")
	}
}

func TestRegisterHashErrorStopsBeforeCreate(t *testing.T) {
	repo := newFakeUserRepo()
	svc := NewUserService(repo, failingHasher{}, nil, nil)
	if err := svc.RegisterUser(registerReq("a@x.com", "password123")); err == nil {
		t.Fatal("expected error")
	}
	if repo.createCalls != 0 {
		t.Error("CreateUser was called after hash failure")
	}
}

// ---------- LoginUser ----------

func registeredService(t *testing.T) (*fakeUserRepo, *userService, *countingHasher) {
	t.Helper()
	repo := newFakeUserRepo()
	svc, h := newTestService(repo)
	if err := svc.RegisterUser(registerReq("a@x.com", "password123")); err != nil {
		t.Fatal(err)
	}
	return repo, svc, h
}

func TestLoginSuccessAndEmailCase(t *testing.T) {
	repo, svc, _ := registeredService(t)
	login(t, svc, "a@x.com", "password123")
	repo.getCalls = nil
	login(t, svc, " A@X.com ", "password123")
	if repo.getCalls[0] != "a@x.com" {
		t.Errorf("lookup email = %q, want a@x.com", repo.getCalls[0])
	}
}

func TestLoginWrongPassword(t *testing.T) {
	_, svc, _ := registeredService(t)
	_, err := svc.LoginUser(context.Background(), dto.LoginUserRequest{Email: "a@x.com", Password: "nope-nope"})
	var target *model.InvalidCredentialsError
	if !errors.As(err, &target) {
		t.Errorf("error = %v, want InvalidCredentialsError", err)
	}
}

func TestLoginUnknownEmailDoesDummyCompare(t *testing.T) {
	_, svc, h := registeredService(t)
	before := h.compares
	_, err := svc.LoginUser(context.Background(), dto.LoginUserRequest{Email: "nobody@x.com", Password: "password123"})
	var target *model.InvalidCredentialsError
	if !errors.As(err, &target) {
		t.Fatalf("error = %v, want InvalidCredentialsError", err)
	}
	if h.compares != before+1 {
		t.Errorf("Compare calls = %d, want 1 (dummy compare)", h.compares-before)
	}
}

func TestLoginLegacyPlaintextNeverSucceeds(t *testing.T) {
	repo, svc, _ := registeredService(t)
	repo.users["old@x.com"] = &model.User{Email: "old@x.com", PasswordHash: "oldpassword"}
	_, err := svc.LoginUser(context.Background(), dto.LoginUserRequest{Email: "old@x.com", Password: "oldpassword"})
	var target *model.InvalidCredentialsError
	if !errors.As(err, &target) {
		t.Errorf("error = %v, want InvalidCredentialsError", err)
	}
}

func TestLoginRepoErrorIsNotInvalidCredentials(t *testing.T) {
	repo := newFakeUserRepo()
	repo.getErr = errors.New("db down")
	svc, _ := newTestService(repo)
	_, err := svc.LoginUser(context.Background(), dto.LoginUserRequest{Email: "a@x.com", Password: "password123"})
	var creds *model.InvalidCredentialsError
	if errors.As(err, &creds) {
		t.Error("db failure was reported as invalid credentials")
	}
	if !errors.Is(err, repo.getErr) {
		t.Errorf("error = %v, want wrapped db error", err)
	}
}

// If the dummy hash cannot be made, login must still work and still reject.
func TestNewUserServiceWithFailingHasherLeavesDummyEmpty(t *testing.T) {
	svc := NewUserService(newFakeUserRepo(), failingHasher{}, nil, nil)
	if svc.dummyHash != "" {
		t.Errorf("dummyHash = %q, want empty", svc.dummyHash)
	}
	_, err := svc.LoginUser(context.Background(), dto.LoginUserRequest{Email: "x@x.com", Password: "pw"})
	var target *model.InvalidCredentialsError
	if !errors.As(err, &target) {
		t.Errorf("error = %v, want InvalidCredentialsError", err)
	}
}

func TestNormalizeEmail(t *testing.T) {
	if got := normalizeEmail("  MiXed@Case.COM\t"); got != "mixed@case.com" {
		t.Errorf("normalizeEmail = %q", got)
	}
}
