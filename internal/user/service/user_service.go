package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/dto"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/model"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/repository"
)

type UserService interface {
	RegisterUser(req dto.ResgisterUserRequest) error
	LoginUser(req dto.LoginUserRequest) error
	VerifyOTP(req dto.VerifyOTPRequest) (string, error)
}

type userService struct {
	userRepo repository.UserRepository
}

func NewUserService(userRepo repository.UserRepository) *userService {
	return &userService{userRepo: userRepo}
}

func (s *userService) RegisterUser(req dto.ResgisterUserRequest) error {
	existingUser, err := s.userRepo.GetUserByEmail(req.Email)
	if err != nil {
		return fmt.Errorf("failed to check existing user: %w", err)
	}
	if existingUser != nil {
		return errors.New("user with this email already exists")
	}
	// TODO: hash password and generate OTP
	user := &model.User{
		ID:           uuid.New().String(),
		FirstName:    req.FirstName,
		LastName:     req.LastName,
		Email:        req.Email,
		PasswordHash: req.Password, // ⚠️ plain for now
		PhoneNumber:  req.PhoneNumber,
		IsAdmin:      false,
		IsVerified:   true, // skip OTP for now
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	return s.userRepo.CreateUser(user)
}

func (s *userService) LoginUser(req dto.LoginUserRequest) error {
	user, err := s.userRepo.GetUserByEmail(req.Email)
	if err != nil {
		return err
	}

	if user == nil {
		return errors.New("user not found")
	}

	//  plain password check (temporary)
	if user.PasswordHash != req.Password {
		return errors.New("invalid credentials")
	}

	// In future → generate OTP here
	return nil
}

func (s *userService) VerifyOTP(req dto.VerifyOTPRequest) (string, error) {
	//  TEMP: skip OTP validation

	user, err := s.userRepo.GetUserByEmail(req.Email)
	if err != nil {
		return "", err
	}

	if user == nil {
		return "", errors.New("user not found")
	}

	// Return dummy token (so frontend / flow works)
	token := fmt.Sprintf("dummy-token-for-%s", user.Email)

	return token, nil
}
