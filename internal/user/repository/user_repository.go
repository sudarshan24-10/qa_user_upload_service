package repository

import (
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/model"
)

type UserRepository interface {
	CreateUser(user *model.User) error
	GetUserByEmail(email string) (*model.User, error)
}
