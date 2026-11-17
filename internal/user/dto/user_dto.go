package dto

type ResgisterUserRequest struct {
	FirstName string `json:"first_name" binding:"required"`
	LastName  string `json:"last_name" binding:"required"`
	// Email is only checked for format here. Trimming and lowercasing happen in
	// the service, not in this DTO.
	Email string `json:"email" binding:"required,email"`
	// Password must be 8..72. The upper bound exists because bcrypt only reads
	// the first 72 bytes. Note this tag counts characters while bcrypt counts
	// bytes (a multi-byte character is 1 char but several bytes), so the service
	// re-checks the byte length.
	Password    string `json:"password" binding:"required,min=8,max=72"`
	PhoneNumber string `json:"phone_number" binding:"required"`
}

type LoginUserRequest struct {
	// Email is only checked for format here; the service normalizes it.
	Email string `json:"email" binding:"required,email"`
	// Login has an upper bound only, with no minimum, so accounts created under
	// the older, looser password rule can still log in.
	Password string `json:"password" binding:"required,max=72"`
}

// RefreshTokenRequest is the body of POST /users/refresh.
type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// LogoutRequest is the body of POST /users/logout.
type LogoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// AuthResponse is returned by login (and later refresh). Message is kept so older
// clients that only read "message" still work; the token fields are additive.
type AuthResponse struct {
	Message      string `json:"message,omitempty"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"` // always "Bearer"
	ExpiresIn    int    `json:"expires_in"` // access token lifetime in seconds (900)
}
