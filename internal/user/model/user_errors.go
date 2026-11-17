package model

// ==================== User / Auth Errors ====================
//
// Typed errors returned by the user service (signup/login). The handler maps
// them to HTTP statuses with errors.As, the same way document errors are
// mapped in httpStatusFromError. Pointer receivers are used to match
// internal/document/model/document_model.go, so callers must errors.As into
// a pointer variable, e.g. var e *model.InvalidCredentialsError.

// EmailAlreadyExistsError indicates a signup was attempted with an email that
// already belongs to a registered account.
// Returned by: the user service (Signup), when the repository reports the
// email as taken (including a unique-constraint race between two requests).
// HTTP mapping: 409 Conflict.
type EmailAlreadyExistsError struct{}

func (e *EmailAlreadyExistsError) Error() string {
	return "an account with this email already exists"
}

// InvalidCredentialsError indicates a login failed because the email/password
// pair is not valid.
// It is deliberately used for BOTH "unknown email" and "wrong password" with
// the same message, so a caller cannot probe which emails have accounts.
// Returned by: the user service (Login).
// HTTP mapping: 401 Unauthorized.
type InvalidCredentialsError struct{}

func (e *InvalidCredentialsError) Error() string {
	return "invalid email or password"
}

// InvalidPasswordError indicates a password was rejected by validation before
// hashing (for example too short, or longer than bcrypt's 72-byte limit).
// Reason carries the exact user-facing message chosen by the service, such as
// "password must be at most 72 bytes", so one type covers every rule.
// Returned by: the user service (Signup).
// HTTP mapping: 400 Bad Request.
type InvalidPasswordError struct {
	Reason string
}

func (e *InvalidPasswordError) Error() string {
	return e.Reason
}

// InvalidRefreshTokenError indicates a refresh token could not be used: it is
// unknown, expired, already used (rotated), or revoked. One message for every case
// on purpose, so a caller cannot tell which one it was.
// Returned by: the user service (RefreshToken).
// HTTP mapping: 401 Unauthorized.
type InvalidRefreshTokenError struct{}

func (e *InvalidRefreshTokenError) Error() string {
	return "invalid or expired refresh token"
}
