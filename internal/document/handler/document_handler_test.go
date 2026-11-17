package handler

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/model"
)

func TestHttpStatusFromError_UploadVerificationErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"size mismatch", &model.FileSizeMismatchError{Declared: 1, Actual: 2}, http.StatusUnprocessableEntity},
		{"content mismatch", &model.FileContentMismatchError{ContentType: "application/pdf"}, http.StatusUnprocessableEntity},
		{"wrapped size mismatch", fmt.Errorf("wrap: %w", &model.FileSizeMismatchError{}), http.StatusUnprocessableEntity},
		{"oversized", &model.FileSizeExceededError{Size: 2, MaxSize: 1}, http.StatusBadRequest},
	}
	for _, c := range cases {
		if got := httpStatusFromError(c.err); got != c.want {
			t.Fatalf("%s: expected %d, got %d", c.name, c.want, got)
		}
	}
}
