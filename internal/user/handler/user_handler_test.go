package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/dto"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/model"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/service"
)

// fakeUserService implements service.UserService; each method returns the configured error.
type fakeUserService struct {
	registerErr error
	loginErr    error
}

func (f *fakeUserService) RegisterUser(dto.ResgisterUserRequest) error { return f.registerErr }

// LoginUser returns fixed tokens on success so the handler's response can be checked.
func (f *fakeUserService) LoginUser(context.Context, dto.LoginUserRequest) (*service.AuthTokens, error) {
	if f.loginErr != nil {
		return nil, f.loginErr
	}
	return &service.AuthTokens{AccessToken: "access-123", RefreshToken: "refresh-456", ExpiresIn: 900}, nil
}

// RefreshToken and Logout are not used by the handlers yet (they come with the
// routes); they exist only so the fake satisfies service.UserService.
func (f *fakeUserService) RefreshToken(context.Context, string) (*service.AuthTokens, error) {
	return nil, nil
}
func (f *fakeUserService) Logout(context.Context, string) error { return nil }

// do sends a JSON body to the register or login handler and returns the recorder.
func do(t *testing.T, svc *fakeUserService, login bool, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewUserHandler(svc)
	r := gin.New()
	r.POST("/register", h.Register)
	r.POST("/login", h.Login)

	path := "/register"
	if login {
		path = "/login"
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// singleJSON decodes the body and fails if there is trailing data (a doubled write).
func singleJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(w.Body.String()))
	var out map[string]string
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("body is not JSON: %q", w.Body.String())
	}
	if dec.More() {
		t.Fatalf("more than one JSON value written: %q", w.Body.String())
	}
	return out
}

func registerBody(first, email, password string) string {
	return fmt.Sprintf(`{"first_name":%q,"last_name":"L","email":%q,"password":%q,"phone_number":"1"}`, first, email, password)
}

// ---------- Register ----------

func TestRegisterSuccess(t *testing.T) {
	w := do(t, &fakeUserService{}, false, registerBody("A", "a@x.com", "password123"))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d", w.Code)
	}
	if got := singleJSON(t, w)["message"]; got != "User registered successfully" {
		t.Errorf("message = %q", got)
	}
}

func TestRegisterServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantMsg    string
	}{
		{"conflict", &model.EmailAlreadyExistsError{}, 409, "an account with this email already exists"},
		{"invalid password", &model.InvalidPasswordError{Reason: "password must be at most 72 bytes"}, 400, "password must be at most 72 bytes"},
		{"wrapped conflict", fmt.Errorf("x: %w", &model.EmailAlreadyExistsError{}), 409, "an account with this email already exists"},
		{"wrapped invalid password", fmt.Errorf("x: %w", &model.InvalidPasswordError{Reason: "bad"}), 400, "bad"},
		{"wrapped credentials", fmt.Errorf("x: %w", &model.InvalidCredentialsError{}), 401, "invalid email or password"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := do(t, &fakeUserService{registerErr: tt.err}, false, registerBody("A", "a@x.com", "password123"))
			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if got := singleJSON(t, w)["error"]; got != tt.wantMsg {
				t.Errorf("error = %q, want %q", got, tt.wantMsg)
			}
		})
	}
}

func TestRegisterValidationMessages(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{"missing first_name", registerBody("", "a@x.com", "password123"), "first_name is required"},
		{"bad email", registerBody("A", "not-an-email", "password123"), "email is not a valid email address"},
		{"7 char password", registerBody("A", "a@x.com", strings.Repeat("a", 7)), "password must be between 8 and 72 characters"},
		{"73 char password", registerBody("A", "a@x.com", strings.Repeat("a", 73)), "password must be between 8 and 72 characters"},
		{"malformed json", `{"first_name":`, "invalid request body"},
		{"not json", `hello`, "invalid request body"},
		{"wrong type", `{"first_name": 5}`, "invalid request body"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := do(t, &fakeUserService{}, false, tt.body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", w.Code)
			}
			if got := singleJSON(t, w)["error"]; got != tt.wantMsg {
				t.Errorf("error = %q, want %q", got, tt.wantMsg)
			}
		})
	}
}

func TestRegisterInternalErrorIsGeneric(t *testing.T) {
	secret := "pq: connection to db-host.internal failed"
	w := do(t, &fakeUserService{registerErr: errors.New(secret)}, false, registerBody("A", "a@x.com", "password123"))
	if w.Code != 500 {
		t.Fatalf("status = %d", w.Code)
	}
	if got := singleJSON(t, w)["error"]; got != "internal server error" {
		t.Errorf("error = %q", got)
	}
	if strings.Contains(w.Body.String(), "db-host") {
		t.Error("response leaked the underlying error text")
	}
}

func TestResponsesDoNotEchoPassword(t *testing.T) {
	pw := "my-very-secret-pw"
	for name, w := range map[string]*httptest.ResponseRecorder{
		"register ok":  do(t, &fakeUserService{}, false, registerBody("A", "a@x.com", pw)),
		"register 409": do(t, &fakeUserService{registerErr: &model.EmailAlreadyExistsError{}}, false, registerBody("A", "a@x.com", pw)),
		"register 500": do(t, &fakeUserService{registerErr: errors.New("boom " + pw)}, false, registerBody("A", "a@x.com", pw)),
		"login ok":     do(t, &fakeUserService{}, true, `{"email":"a@x.com","password":"`+pw+`"}`),
		"login 401":    do(t, &fakeUserService{loginErr: &model.InvalidCredentialsError{}}, true, `{"email":"a@x.com","password":"`+pw+`"}`),
	} {
		if strings.Contains(w.Body.String(), pw) {
			t.Errorf("%s: response contains the password", name)
		}
	}
}

// ---------- Login ----------

func TestLoginSuccess(t *testing.T) {
	w := do(t, &fakeUserService{}, true, `{"email":"a@x.com","password":"password123"}`)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	// Decode into a generic map: expires_in is a number, not a string.
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %q", w.Body.String())
	}
	want := map[string]any{
		"message":       "login successful", // kept for older clients
		"access_token":  "access-123",
		"refresh_token": "refresh-456",
		"token_type":    "Bearer",
		"expires_in":    float64(900), // JSON numbers decode as float64
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("%s = %v, want %v", k, body[k], v)
		}
	}
	if len(body) != len(want) {
		t.Errorf("unexpected fields in login response: %v", body)
	}
	// Responses that carry tokens must never be cached.
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

// An invalid or expired refresh token maps to 401 with the generic message, also
// when wrapped. (The refresh/logout routes are added in a later part.)
func TestInvalidRefreshTokenMapsTo401(t *testing.T) {
	for _, err := range []error{
		&model.InvalidRefreshTokenError{},
		fmt.Errorf("wrapped: %w", &model.InvalidRefreshTokenError{}),
	} {
		status, msg := userStatusFromError(err)
		if status != http.StatusUnauthorized || msg != "invalid or expired refresh token" {
			t.Errorf("got %d %q", status, msg)
		}
	}
}

// Unknown email and wrong password both surface as InvalidCredentialsError, so the
// client must see byte-identical replies.
func TestLoginUnknownEmailAndWrongPasswordIdentical(t *testing.T) {
	a := do(t, &fakeUserService{loginErr: &model.InvalidCredentialsError{}}, true, `{"email":"nobody@x.com","password":"password123"}`)
	b := do(t, &fakeUserService{loginErr: fmt.Errorf("wrapped: %w", &model.InvalidCredentialsError{})}, true, `{"email":"a@x.com","password":"wrong-password"}`)
	if a.Code != 401 || b.Code != 401 {
		t.Fatalf("status = %d / %d, want 401", a.Code, b.Code)
	}
	if a.Body.String() != b.Body.String() {
		t.Errorf("bodies differ: %q vs %q", a.Body.String(), b.Body.String())
	}
	if got := singleJSON(t, a)["error"]; got != "invalid email or password" {
		t.Errorf("error = %q", got)
	}
}

func TestLoginValidation(t *testing.T) {
	w := do(t, &fakeUserService{}, true, `{"email":"a@x.com","password":"`+strings.Repeat("a", 73)+`"}`)
	if w.Code != 400 {
		t.Fatalf("status = %d", w.Code)
	}
	if got := singleJSON(t, w)["error"]; got != "password must be at most 72 characters" {
		t.Errorf("error = %q", got)
	}

	// A short login password is allowed by the DTO and reaches the service (-> 200 here).
	if w := do(t, &fakeUserService{}, true, `{"email":"a@x.com","password":"abc"}`); w.Code != 200 {
		t.Errorf("short login password status = %d, want 200", w.Code)
	}

	// Missing password and malformed body.
	w = do(t, &fakeUserService{}, true, `{"email":"a@x.com"}`)
	if got := singleJSON(t, w)["error"]; w.Code != 400 || got != "password is required" {
		t.Errorf("missing password: %d %q", w.Code, got)
	}
	w = do(t, &fakeUserService{}, true, `{`)
	if got := singleJSON(t, w)["error"]; w.Code != 400 || got != "invalid request body" {
		t.Errorf("malformed: %d %q", w.Code, got)
	}
}

func TestLoginInfrastructureErrorIsNot401(t *testing.T) {
	w := do(t, &fakeUserService{loginErr: fmt.Errorf("failed to look up user: %w", errors.New("db down"))}, true, `{"email":"a@x.com","password":"password123"}`)
	if w.Code != 500 {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if got := singleJSON(t, w)["error"]; got != "internal server error" {
		t.Errorf("error = %q", got)
	}
	if strings.Contains(w.Body.String(), "db down") {
		t.Error("response leaked the underlying error text")
	}
}

// ---------- userStatusFromError ----------

func TestUserStatusFromError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantMsg    string
	}{
		{"email exists", &model.EmailAlreadyExistsError{}, 409, "an account with this email already exists"},
		{"invalid credentials", &model.InvalidCredentialsError{}, 401, "invalid email or password"},
		{"invalid password", &model.InvalidPasswordError{Reason: "too short"}, 400, "too short"},
		{"wrapped", fmt.Errorf("a: %w", fmt.Errorf("b: %w", &model.EmailAlreadyExistsError{})), 409, "an account with this email already exists"},
		{"unknown", errors.New("secret detail"), 500, "internal server error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, msg := userStatusFromError(tt.err)
			if status != tt.wantStatus || msg != tt.wantMsg {
				t.Errorf("got (%d, %q), want (%d, %q)", status, msg, tt.wantStatus, tt.wantMsg)
			}
		})
	}
}

// validationMessage falls back to "<field> is invalid" for rules without a custom text.
func TestValidationMessageFallbackBodyOnly(t *testing.T) {
	if got := validationMessage(errors.New("not a validator error"), registerPasswordMessage); got != "invalid request body" {
		t.Errorf("got %q", got)
	}
}

// A rule with no custom text (here "gte" on a field missing from jsonFieldNames) falls
// back to "<field> is invalid" instead of leaking validator internals.
func TestValidationMessageDefaultBranch(t *testing.T) {
	type odd struct {
		Age int `validate:"gte=18"`
	}
	err := validator.New().Struct(odd{Age: 1})
	if got := validationMessage(err, registerPasswordMessage); got != "Age is invalid" {
		t.Errorf("got %q, want %q", got, "Age is invalid")
	}
}
