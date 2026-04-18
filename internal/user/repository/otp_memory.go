package repository

import (
	"errors"
	"sync"
	"time"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/model"
)

type OTPMemoryRepository struct {
	store map[string]model.Otp
	mu    sync.RWMutex
}

func NewOTPMemoryRepository() *OTPMemoryRepository {
	return &OTPMemoryRepository{store: make(map[string]model.Otp)}
}

func (r *OTPMemoryRepository) Save(email string, code string, expiry time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store[email] = model.Otp{ID: email, Email: email, Code: code, ExpiresAt: time.Now().Add(expiry)}
	return nil
}

func (r *OTPMemoryRepository) Get(email string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	otp, exists := r.store[email]
	if !exists {
		return "", errors.New("otp not found")
	}

	if time.Now().After(otp.ExpiresAt) {
		return "", errors.New("otp expired")
	}

	return otp.Code, nil
}

func (r *OTPMemoryRepository) Delete(email string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.store, email)
	return nil
}
