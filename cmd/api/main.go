package main

import (
	"github.com/sudarshan24-10/qa_user_upload_service/internal/app"
)

func main() {
	app := app.NewApp()
	app.Run()
}
