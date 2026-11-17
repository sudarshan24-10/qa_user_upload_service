package service

import (
	"context"

	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/dto"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/model"
)

type DocumentService interface {
	InitiateUpload(ctx context.Context, req dto.InitiateUploadRequest) (*dto.InitiateUploadResponse, error)
	CompleteUpload(ctx context.Context, req dto.CompleteUploadRequest) (*dto.CompleteUploadResponse, error)
	GetDocument(ctx context.Context, req dto.GetDocumentRequest) (*dto.GetDocumentResponse, error)
	MarkDocumentFailed(ctx context.Context, documentID string, userID string, failureStage model.FailureStage, failureReason string) error
	FinalizeUpload(ctx context.Context, documentID string) error
}
