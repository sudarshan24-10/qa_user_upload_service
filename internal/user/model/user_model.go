package model

import "time"

type User struct {
	ID           string
	FirstName    string
	LastName     string
	Email        string
	PasswordHash string
	PhoneNumber  string
	IsAdmin      bool
	IsVerified   bool
	GoogleID     string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
