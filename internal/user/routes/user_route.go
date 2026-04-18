package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/user/handler"
)

func RegisterUserRoutes(router *gin.Engine, userHandler *handler.UserHandler) {
	userGroup := router.Group("/api/v1/users")
	userGroup.POST("/register", userHandler.Register)
	userGroup.POST("/login", userHandler.Login)
	userGroup.POST("/verify_otp", userHandler.VerifyOTP)
}
