package service

import (
	"context"

	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/dto"
)

type DocumentService interface {
	InitiateUpload(ctx context.Context, req dto.InitiateUploadRequest) (*dto.InitiateUploadResponse, error)
	CompleteUpload(ctx context.Context, req dto.CompleteUploadRequest) (*dto.CompleteUploadResponse, error)
	GetDocument(ctx context.Context, req dto.GetDocumentRequest) (*dto.GetDocumentResponse, error)
	FailUpload(ctx context.Context, documentID string, userID string, idempotencyKey string, failedReason string) error
	FinilizeUpload(ctx context.Context, documentID string) error
}
