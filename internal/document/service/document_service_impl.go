package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/config"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/dto"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/model"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/repository"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/storage"
	outboxdto "github.com/sudarshan24-10/qa_user_upload_service/internal/outbox/dto"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/outbox/helper"
	outboxModel "github.com/sudarshan24-10/qa_user_upload_service/internal/outbox/model"
	outboxRepo "github.com/sudarshan24-10/qa_user_upload_service/internal/outbox/repository"
)

type documentService struct {
	repo       repository.DocumentRepository
	outboxRepo outboxRepo.OutboxRepository
	validate   *validator.Validate
	storage    storage.ObjectStorage
	config     *config.Config
}

// FinilizeUpload implements [DocumentService].
func (s *documentService) FinilizeUpload(ctx context.Context, documentID string) error {
	panic("unimplemented")
}

func NewDocumentService(
	repo repository.DocumentRepository,
	outboxRepo outboxRepo.OutboxRepository,
	validate *validator.Validate,
	storage storage.ObjectStorage,
) *documentService {
	return &documentService{
		repo:       repo,
		outboxRepo: outboxRepo,
		validate:   validate,
		storage:    storage,
		config:     config.Load(),
	}
}

func (s *documentService) InitiateUpload(ctx context.Context, req dto.InitiateUploadRequest) (*dto.InitiateUploadResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, err
	}

	allowed := map[string]bool{
		"application/pdf": true,
		"image/jpeg":      true,
	}
	if !allowed[req.ContentType] {
		return nil, fmt.Errorf("unsupported content type")
	}

	if req.Size > s.config.MAX_SIZE {
		return nil, fmt.Errorf("file size exceeds limit")
	}

	documentID := uuid.New().String()
	objectKey := generateObjectKey(req.UserID, documentID, req.FileName)

	doc := &model.Document{
		ID:              documentID,
		UserID:          req.UserID,
		FileName:        req.FileName,
		ObjectKey:       objectKey,
		Size:            req.Size,
		Status:          model.StatusInitiated,
		IdempotencyKey:  req.IdempotencyKey,
		ContentType:     req.ContentType,
		Metadata:        req.Metadata,
		UploadExpiresAt: time.Now().Add(15 * time.Minute),
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	docCreated, err := s.repo.Create(ctx, doc)
	if err != nil {
		return nil, err
	}

	uploadURL, err := s.storage.GenerateUploadURL(ctx, docCreated.ObjectKey, req.ContentType, 15*time.Minute)
	if err != nil {
		return nil, err
	}

	return &dto.InitiateUploadResponse{
		DocumentID:      docCreated.ID,
		UploadURL:       uploadURL,
		ObjectKey:       docCreated.ObjectKey,
		UploadExpiresAt: docCreated.UploadExpiresAt.Unix(),
	}, nil
}

func (s *documentService) CompleteUpload(ctx context.Context, req dto.CompleteUploadRequest) (*dto.CompleteUploadResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, err
	}

	doc, err := s.repo.GetByID(ctx, req.DocumentID)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, fmt.Errorf("document not found")
	}

	if doc.UserID != req.UserID {
		return nil, fmt.Errorf("unauthorized access")
	}

	if doc.Status == model.StatusUploaded {
		return &dto.CompleteUploadResponse{
			DocumentID: req.DocumentID,
			Status:     string(model.StatusUploaded),
		}, nil
	}
	log.Printf("Document  %s updated with current status: %s", doc.ID, doc.Status)

	if doc.Status != model.StatusInitiated {
		return nil, fmt.Errorf("invalid document state transition")
	}

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if err := s.repo.UpdateStatusWithTx(ctx, tx, req.DocumentID, model.StatusUploaded); err != nil {
		return nil, err
	}

	eventID := uuid.New().String()

	eventPayload := outboxdto.DocumentUploadedEvent{
		EventID:        eventID,
		DocumentID:     doc.ID,
		UserID:         doc.UserID,
		ObjectKey:      doc.ObjectKey,
		FileName:       doc.FileName,
		ContentType:    doc.ContentType,
		IdempotencyKey: doc.IdempotencyKey,
	}

	payloadMap, err := helper.ToMap(eventPayload)
	if err != nil {
		return nil, err
	}

	payloadJSON, err := json.Marshal(payloadMap)
	if err != nil {
		return nil, err
	}

	outboxEvent := &outboxModel.OutboxEvent{
		ID:          eventID,
		AggregateID: doc.ID,
		EventType:   "DOCUMENT_UPLOADED",
		Payload:     string(payloadJSON),
		Status:      outboxModel.StatusPending,
		RetryCount:  0,
		CreatedAt:   time.Now(),
	}

	if err := s.outboxRepo.CreateWithTx(ctx, tx, outboxEvent); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	log.Println("Outbox event created:", eventID)

	return &dto.CompleteUploadResponse{
		DocumentID: req.DocumentID,
		Status:     string(model.StatusUploaded),
	}, nil
}

func (s *documentService) FinalizeUpload(ctx context.Context, documentID string) error {
	doc, err := s.repo.GetByID(ctx, documentID)
	if err != nil {
		return err
	}
	if doc == nil {
		return fmt.Errorf("document not found")
	}

	if doc.Status == model.StatusUploaded {
		return nil
	}

	exists, err := s.storage.CheckObjectExists(ctx, doc.ObjectKey)
	if err != nil {
		return err
	}

	if !exists {
		failedReason := "file not found in storage"
		return s.repo.UpdateStatus(ctx, documentID, model.StatusFailed, &failedReason)
	}

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := s.repo.UpdateStatusWithTx(ctx, tx, documentID, model.StatusUploaded); err != nil {
		return err
	}

	eventID := uuid.New().String()

	eventPayload := outboxdto.DocumentUploadedEvent{
		EventID:        eventID,
		DocumentID:     doc.ID,
		UserID:         doc.UserID,
		ObjectKey:      doc.ObjectKey,
		FileName:       doc.FileName,
		ContentType:    doc.ContentType,
		IdempotencyKey: doc.IdempotencyKey,
	}

	payloadMap, err := helper.ToMap(eventPayload)
	if err != nil {
		return err
	}

	payloadJSON, err := json.Marshal(payloadMap)
	if err != nil {
		return err
	}

	outboxEvent := &outboxModel.OutboxEvent{
		ID:          eventID,
		AggregateID: doc.ID,
		EventType:   "DOCUMENT_UPLOADED",
		Payload:     string(payloadJSON),
		Status:      outboxModel.StatusPending,
		RetryCount:  0,
		CreatedAt:   time.Now(),
	}

	if err := s.outboxRepo.CreateWithTx(ctx, tx, outboxEvent); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *documentService) FailUpload(ctx context.Context, documentID string, userID string, idempotencyKey string, failedReason string) error {
	log.Printf("Marking document %s as failed for user %s due to: %s", documentID, userID, failedReason)

	doc, err := s.repo.GetByID(ctx, documentID)
	if err != nil {
		return err
	}
	if doc == nil {
		return fmt.Errorf("document not found")
	}
	reasonPtr := &failedReason
	return s.repo.UpdateStatus(ctx, documentID, model.StatusFailed, reasonPtr)
}

func (s *documentService) GetDocument(ctx context.Context, req dto.GetDocumentRequest) (*dto.GetDocumentResponse, error) {
	doc, err := s.repo.GetByID(ctx, req.DocumentID)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, fmt.Errorf("document not found")
	}
	if doc.UserID != req.UserID {
		return nil, fmt.Errorf("unauthorized access")
	}

	return &dto.GetDocumentResponse{
		DocumentID: doc.ID,
		UserID:     doc.UserID,
		FileName:   doc.FileName,
		Size:       doc.Size,
		Status:     string(doc.Status),
		Metadata:   doc.Metadata,
		CreatedAt:  doc.CreatedAt.Unix(),
		UpdatedAt:  doc.UpdatedAt.Unix(),
	}, nil
}

func generateObjectKey(userID, documentID, fileName string) string {
	return fmt.Sprintf("%s/%s/%s", userID, documentID, fileName)
}
