package repository

import (
	"time"
)

type OTPRepository interface {
	Save(email string, code string, expiry time.Duration) error
	Get(email string) (string, error)
	Delete(email string) error
}
