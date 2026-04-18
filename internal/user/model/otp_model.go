package model

import "time"

type Otp struct {
	ID        string
	Email     string
	Code      string
	ExpiresAt time.Time
}
