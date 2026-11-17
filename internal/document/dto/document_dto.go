package dto

type InitiateUploadRequest struct {
	UserID         string            `json:"user_id" validate:"required"`
	FileName       string            `json:"file_name" validate:"required,max=255"`
	ContentType    string            `json:"content_type" validate:"required"`
	Size           int64             `json:"size" validate:"gt=0"`
	Metadata       map[string]string `json:"metadata"`
	IdempotencyKey string            `json:"idempotency_key" validate:"required"`
}

type InitiateUploadResponse struct {
	DocumentID      string `json:"document_id"`
	UploadURL       string `json:"upload_url"`
	ObjectKey       string `json:"object_key"`
	UploadExpiresAt int64  `json:"upload_expires_at"`
}

type CompleteUploadRequest struct {
	DocumentID     string `json:"document_id" validate:"required"`
	UserID         string `json:"user_id" validate:"required"`
	IdempotencyKey string `json:"idempotency_key" validate:"required"`
}

type CompleteUploadResponse struct {
	DocumentID string `json:"document_id"`
	Status     string `json:"status"`
}

type MarkDocumentFailedRequest struct {
	DocumentID    string `json:"document_id" validate:"required"`
	UserID        string `json:"user_id" validate:"required"`
	FailureStage  string `json:"failure_stage" validate:"required"`
	FailureReason string `json:"failure_reason" validate:"required"`
}

type GetDocumentRequest struct {
	DocumentID string `json:"document_id" validate:"required"`
	UserID     string `json:"user_id" validate:"required"`
}

type GetDocumentResponse struct {
	DocumentID string            `json:"document_id"`
	UserID     string            `json:"user_id"`
	FileName   string            `json:"file_name"`
	Size       int64             `json:"size"`
	Status     string            `json:"status"`
	Metadata   map[string]string `json:"metadata"`
	CreatedAt  int64             `json:"created_at"`
	UpdatedAt  int64             `json:"updated_at"`
}
