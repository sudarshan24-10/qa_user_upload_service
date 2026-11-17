package service

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/dto"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/model"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/repository"
)

// AuthTokens is what login and refresh hand back: a short-lived access token (JWT)
// and a long-lived refresh token. RefreshToken is the RAW value, shown to the client
// once; only its hash is stored.
type AuthTokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int // access token lifetime in seconds
}

type UserService interface {
	RegisterUser(req dto.ResgisterUserRequest) error
	LoginUser(ctx context.Context, req dto.LoginUserRequest) (*AuthTokens, error)
	// RefreshToken exchanges a refresh token for a new access + refresh token pair.
	RefreshToken(ctx context.Context, refreshToken string) (*AuthTokens, error)
	// Logout revokes the whole login session the refresh token belongs to.
	Logout(ctx context.Context, refreshToken string) error
}

type userService struct {
	userRepo repository.UserRepository
	// tokens signs access tokens; refreshRepo stores refresh token hashes.
	tokens      *TokenService
	refreshRepo repository.RefreshTokenRepository
	// hasher hashes passwords on register and verifies them on login.
	hasher PasswordHasher
	// dummyHash is a real hash of a throwaway password. Login compares against it
	// when the email is unknown, so an unknown email costs the same time as a wrong
	// password and timing does not reveal which emails have accounts.
	dummyHash string
}

func NewUserService(userRepo repository.UserRepository, hasher PasswordHasher, tokens *TokenService, refreshRepo repository.RefreshTokenRepository) *userService {
	// Hash the dummy password once at startup, not on every login.
	// If hashing fails we leave dummyHash empty: comparing against it then fails
	// fast, so only the timing equalization is lost. Login still works correctly.
	dummyHash, err := hasher.Hash("dummy-password-for-timing")
	if err != nil {
		log.Printf("could not create dummy hash for login timing: %v", err)
		dummyHash = ""
	}
	return &userService{userRepo: userRepo, hasher: hasher, dummyHash: dummyHash, tokens: tokens, refreshRepo: refreshRepo}
}

// normalizeEmail trims surrounding spaces and lowercases the email. It is applied
// on every register and login path before any lookup or insert. The database
// unique index is case-sensitive, so normalizing once here guarantees two emails
// that differ only by case or spaces cannot become two accounts.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (s *userService) RegisterUser(req dto.ResgisterUserRequest) error {
	// Step 1: normalize the email so lookup and the stored value are consistent.
	email := normalizeEmail(req.Email)

	// Step 2: reject passwords bcrypt cannot handle, before touching the database.
	// The DTO counts characters but bcrypt counts bytes, so a short multi-byte
	// password can still exceed 72 bytes. bcrypt would otherwise ignore the extra
	// bytes silently, so we refuse instead of truncating.
	if len(req.Password) > maxPasswordBytes {
		return &model.InvalidPasswordError{Reason: "password must be at most 72 bytes"}
	}

	// Step 3: reject an email that is already registered.
	existingUser, err := s.userRepo.GetUserByEmail(email)
	if err != nil {
		return fmt.Errorf("failed to check existing user: %w", err)
	}
	if existingUser != nil {
		return &model.EmailAlreadyExistsError{}
	}

	// Step 4: hash the password. Only the hash is stored, never the plaintext.
	passwordHash, err := s.hasher.Hash(req.Password)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	user := &model.User{
		ID:           uuid.New().String(),
		FirstName:    req.FirstName,
		LastName:     req.LastName,
		Email:        email,
		PasswordHash: passwordHash,
		PhoneNumber:  req.PhoneNumber,
		IsAdmin:      false,
		IsVerified:   true, // no email verification in v1
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	// The repository error is returned unchanged: when two requests race past the
	// check above, the repository will later map the unique violation to
	// EmailAlreadyExistsError and it must reach the handler intact.
	return s.userRepo.CreateUser(user)
}

func (s *userService) LoginUser(ctx context.Context, req dto.LoginUserRequest) (*AuthTokens, error) {
	// Step 1: normalize the email the same way registration did.
	email := normalizeEmail(req.Email)

	user, err := s.userRepo.GetUserByEmail(email)
	if err != nil {
		// A database failure is not a bad login: wrap it so it becomes a 500.
		return nil, fmt.Errorf("failed to look up user: %w", err)
	}

	// Step 2: unknown email. Do a throwaway compare so this path takes about as
	// long as a wrong password, then return the same error as a wrong password.
	if user == nil {
		_ = s.hasher.Compare(s.dummyHash, req.Password)
		return nil, &model.InvalidCredentialsError{}
	}

	// Step 3: verify the password against the stored hash. A mismatch, or a stored
	// value that is not a bcrypt hash (e.g. an old plaintext row), both fail.
	if err := s.hasher.Compare(user.PasswordHash, req.Password); err != nil {
		return nil, &model.InvalidCredentialsError{}
	}

	// Step 4: the password is right, so start a new login session. Sign the access
	// token first: if signing failed, nothing has been stored yet.
	accessToken, err := s.tokens.IssueAccessToken(user.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to issue access token: %w", err)
	}

	// Step 5: create the first refresh token of a brand-new family. Every token
	// rotated from this one stays in the same family, so a reused (stolen) token can
	// revoke this whole login session at once.
	rawRefresh, refreshHash, err := GenerateRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate refresh token: %w", err)
	}
	err = s.refreshRepo.Create(ctx, &repository.RefreshToken{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		FamilyID:  uuid.New().String(),
		TokenHash: refreshHash,
	}, RefreshTokenTTL)
	if err != nil {
		return nil, fmt.Errorf("failed to store refresh token: %w", err)
	}

	// Step 6: opportunistic cleanup of this user's old expired tokens (the 1 day of
	// grace is applied inside the repository). Best effort: a cleanup failure must
	// not fail a login that already succeeded, so it is only logged.
	if err := s.refreshRepo.DeleteExpiredForUser(ctx, user.ID); err != nil {
		log.Printf("could not delete expired refresh tokens: %v", err)
	}

	return s.newAuthTokens(accessToken, rawRefresh), nil
}

// newAuthTokens packs the two tokens with the access token lifetime in seconds.
func (s *userService) newAuthTokens(accessToken, rawRefresh string) *AuthTokens {
	return &AuthTokens{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		ExpiresIn:    int(AccessTokenTTL.Seconds()),
	}
}

// RefreshToken rotates a refresh token: the presented token is burned and a new
// access + refresh pair is returned. Every failure the client can cause (unknown,
// expired, used, revoked) is the same InvalidRefreshTokenError.
func (s *userService) RefreshToken(ctx context.Context, refreshToken string) (*AuthTokens, error) {
	// Step 1: only the hash is ever looked up or stored; the raw token is not kept.
	oldHash := HashRefreshToken(refreshToken)

	// Step 2: prepare the next token before touching the database, so the
	// transactional Rotate below only has to claim and insert.
	rawNext, nextHash, err := GenerateRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate refresh token: %w", err)
	}

	// Step 3: Rotate claims the old token and stores the next one in one
	// transaction (see the repository). Two simultaneous requests with the same
	// token cannot both win; the loser gets (nil, nil) and lands in step 4.
	old, err := s.refreshRepo.Rotate(ctx, oldHash, &repository.RefreshToken{
		ID:        uuid.New().String(),
		TokenHash: nextHash,
	}, RefreshTokenTTL)
	if err != nil {
		return nil, fmt.Errorf("failed to rotate refresh token: %w", err)
	}

	// Step 4: the token could not be claimed. Example: R1 was already rotated into
	// R2 and someone presents R1 again (a stolen copy, or a second browser tab). If
	// the token exists at all, revoke its whole family so R2 stops working too
	// (reuse detection). A token that was never issued just fails.
	if old == nil {
		family, err := s.refreshRepo.FamilyOf(ctx, oldHash)
		if err != nil {
			return nil, fmt.Errorf("failed to look up refresh token family: %w", err)
		}
		if family != "" {
			if err := s.refreshRepo.RevokeFamily(ctx, family); err != nil {
				return nil, fmt.Errorf("failed to revoke refresh token family: %w", err)
			}
		}
		return nil, &model.InvalidRefreshTokenError{}
	}

	// Step 5: the old token was valid; sign a new access token for its owner. If
	// signing failed here the rotation has already committed, so the client would
	// have to log in again. HS256 signing with a byte key does not fail in practice.
	accessToken, err := s.tokens.IssueAccessToken(old.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to issue access token: %w", err)
	}
	return s.newAuthTokens(accessToken, rawNext), nil
}

// Logout ends the login session the refresh token belongs to by revoking its whole
// family. An unknown token is NOT an error (nothing to revoke, and the reply must
// not reveal whether a token existed). A database failure is still returned, so a
// logout that did not really happen is not reported as success.
// Access tokens are not tracked, so an already-issued one stays valid until it expires.
func (s *userService) Logout(ctx context.Context, refreshToken string) error {
	family, err := s.refreshRepo.FamilyOf(ctx, HashRefreshToken(refreshToken))
	if err != nil {
		return fmt.Errorf("failed to look up refresh token family: %w", err)
	}
	if family == "" {
		return nil
	}
	if err := s.refreshRepo.RevokeFamily(ctx, family); err != nil {
		return fmt.Errorf("failed to revoke refresh token family: %w", err)
	}
	return nil
}
