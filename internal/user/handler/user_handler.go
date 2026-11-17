package handler

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/dto"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/model"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/service"
)

// Messages for the register and login password rules (they differ: login has no minimum).
const (
	registerPasswordMessage = "password must be between 8 and 72 characters"
	loginPasswordMessage    = "password must be at most 72 characters"
	invalidBodyMessage      = "invalid request body"
	internalErrorMessage    = "internal server error"
)

// jsonFieldNames maps the struct field names reported by the validator to the JSON
// names clients actually send, so messages say "first_name", not "FirstName".
var jsonFieldNames = map[string]string{
	"FirstName":   "first_name",
	"LastName":    "last_name",
	"Email":       "email",
	"Password":    "password",
	"PhoneNumber": "phone_number",
}

// validationMessage turns a ShouldBindJSON error into one clear 400 message.
// Only the first problem is reported. A body that is not valid JSON (or has the wrong
// types) is not a validator error, so it becomes the generic "invalid request body".
// passwordMessage is the text to use when the password fails its length rule.
func validationMessage(err error, passwordMessage string) string {
	var validationErrs validator.ValidationErrors
	if !errors.As(err, &validationErrs) || len(validationErrs) == 0 {
		return invalidBodyMessage
	}

	fe := validationErrs[0]
	name, ok := jsonFieldNames[fe.Field()]
	if !ok {
		name = fe.Field()
	}

	switch {
	case fe.Tag() == "required":
		return name + " is required"
	case fe.Field() == "Email" && fe.Tag() == "email":
		return "email is not a valid email address"
	case fe.Field() == "Password" && (fe.Tag() == "min" || fe.Tag() == "max"):
		return passwordMessage
	default:
		return name + " is invalid"
	}
}

// userStatusFromError maps a service error to an HTTP status and a client-safe message.
// errors.As is used so errors wrapped with fmt.Errorf("%w") still map correctly.
// Anything unrecognised is a 500 with a generic message: the raw error can contain
// database details and must never be sent to the client (it is logged by the caller).
func userStatusFromError(err error) (int, string) {
	var emailExists *model.EmailAlreadyExistsError
	var invalidCreds *model.InvalidCredentialsError
	var invalidPassword *model.InvalidPasswordError
	var invalidRefresh *model.InvalidRefreshTokenError

	switch {
	case errors.As(err, &emailExists):
		return http.StatusConflict, emailExists.Error()
	case errors.As(err, &invalidCreds):
		return http.StatusUnauthorized, invalidCreds.Error()
	case errors.As(err, &invalidPassword):
		return http.StatusBadRequest, invalidPassword.Error()
	case errors.As(err, &invalidRefresh):
		return http.StatusUnauthorized, invalidRefresh.Error()
	default:
		return http.StatusInternalServerError, internalErrorMessage
	}
}

// writeUserError writes exactly one error reply for a service error. Unexpected errors
// are logged server-side; the log contains only the error, never the request body,
// password or hash.
func writeUserError(c *gin.Context, err error) {
	status, message := userStatusFromError(err)
	if status == http.StatusInternalServerError {
		log.Printf("user request failed: %v", err)
	}
	c.JSON(status, gin.H{"error": message})
}

type UserHandler struct {
	userService service.UserService
}

func NewUserHandler(userService service.UserService) *UserHandler {
	return &UserHandler{userService: userService}
}

func (h *UserHandler) Register(c *gin.Context) {
	var req dto.ResgisterUserRequest
	// ShouldBindJSON only returns the error; unlike BindJSON it does not write a 400
	// itself, so we write exactly one reply below.
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": validationMessage(err, registerPasswordMessage)})
		return
	}
	err = h.userService.RegisterUser(req)
	if err != nil {
		writeUserError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": "User registered successfully"})
}

func (h *UserHandler) Login(c *gin.Context) {
	var req dto.LoginUserRequest

	// ShouldBindJSON (not BindJSON) so only one reply is written; see Register.
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": validationMessage(err, loginPasswordMessage)})
		return
	}

	tokens, err := h.userService.LoginUser(c.Request.Context(), req)
	if err != nil {
		// Bad credentials -> 401, infrastructure failure -> 500 (not 401).
		writeUserError(c, err)
		return
	}

	// "message" is kept for older clients; the token fields are additive. no-store
	// stops browsers and proxies from caching a response that carries tokens.
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, dto.AuthResponse{
		Message:      "login successful",
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    tokens.ExpiresIn,
	})
}
