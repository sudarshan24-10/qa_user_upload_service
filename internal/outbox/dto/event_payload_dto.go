package dto

type DocumentUploadedEvent struct {
	EventID        string `json:"event_id"`
	DocumentID     string `json:"document_id"`
	UserID         string `json:"user_id"`
	ObjectKey      string `json:"object_key"`
	FileName       string `json:"file_name"`
	ContentType    string `json:"content_type"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}
