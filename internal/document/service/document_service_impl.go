package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path"
	"strings"
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

// objectHeadBytes is how many leading bytes are read to verify file type.
const objectHeadBytes = 1024

type documentService struct {
	repo       repository.DocumentRepository
	outboxRepo outboxRepo.OutboxRepository
	validate   *validator.Validate
	storage    storage.ObjectStorage
	config     *config.Config
}

func NewDocumentService(
	repo repository.DocumentRepository,
	outboxRepo outboxRepo.OutboxRepository,
	validate *validator.Validate,
	storage storage.ObjectStorage,
	cfg *config.Config,
) *documentService {
	return &documentService{
		repo:       repo,
		outboxRepo: outboxRepo,
		validate:   validate,
		storage:    storage,
		config:     cfg,
	}
}

func (s *documentService) InitiateUpload(ctx context.Context, req dto.InitiateUploadRequest) (*dto.InitiateUploadResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, err
	}
	if err := validateIdentifier("user_id", req.UserID); err != nil {
		return nil, err
	}

	allowed := map[string]bool{
		"application/pdf": true,
		"image/jpeg":      true,
	}
	if !allowed[req.ContentType] {
		return nil, &model.UnsupportedContentTypeError{ContentType: req.ContentType}
	}

	if req.Size > s.config.MAX_SIZE {
		return nil, &model.FileSizeExceededError{Size: req.Size, MaxSize: s.config.MAX_SIZE}
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
	if docCreated.UserID != req.UserID ||
		docCreated.FileName != req.FileName ||
		docCreated.ContentType != req.ContentType ||
		docCreated.Size != req.Size {
		return nil, &model.IdempotencyKeyReuseError{IdempotencyKey: req.IdempotencyKey}
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
		return nil, fmt.Errorf("validation error: %w", err)
	}
	if err := validateIdentifier("document_id", req.DocumentID); err != nil {
		return nil, err
	}
	if err := validateIdentifier("user_id", req.UserID); err != nil {
		return nil, err
	}

	// ========== Step 1: Fetch document ==========
	doc, err := s.repo.GetByID(ctx, req.DocumentID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch document: %w", err)
	}
	if doc == nil {
		return nil, &model.DocumentNotFoundError{DocumentID: req.DocumentID}
	}

	// ========== Step 2: Validate authorization ==========
	if doc.UserID != req.UserID {
		return nil, &model.UnauthorizedDocumentError{DocumentID: req.DocumentID}
	}
	if doc.IdempotencyKey != req.IdempotencyKey {
		return nil, &model.IdempotencyKeyMismatchError{DocumentID: req.DocumentID}
	}

	// ========== Step 3: Handle idempotency - if already UPLOADED, return success ==========
	if doc.Status == model.StatusUploaded {
		log.Printf("Document %s already in UPLOADED state (idempotent request)", req.DocumentID)
		return &dto.CompleteUploadResponse{
			DocumentID: req.DocumentID,
			Status:     string(model.StatusUploaded),
		}, nil
	}

	// ========== Step 4: Validate current state is INITIATED ==========
	if doc.Status != model.StatusInitiated {
		return nil, &model.InvalidStateTransitionError{
			DocumentID:      req.DocumentID,
			CurrentStatus:   doc.Status,
			AttemptedStatus: model.StatusUploaded,
		}
	}

	// ========== Step 5: CRITICAL - Verify file actually exists in storage ==========
	// This is the key difference from the old code
	log.Printf("Verifying file exists in storage for document %s (object key: %s)", req.DocumentID, doc.ObjectKey)
	info, err := s.storage.InspectObject(ctx, doc.ObjectKey, objectHeadBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to verify file in storage: %w", err)
	}

	if info == nil {
		// File not in storage - mark as failed
		log.Printf("File not found in storage for document %s, marking as FAILED", req.DocumentID)
		changed, failErr := s.repo.TransitionInitiatedToFailed(ctx, req.DocumentID, model.FailureStageUpload, "file not found in storage during completion")
		if failErr != nil {
			log.Printf("Failed to mark document %s as FAILED: %v", req.DocumentID, failErr)
		} else if !changed {
			log.Printf("Document %s changed state concurrently; not marking FAILED", req.DocumentID)
		}
		return nil, &model.FileNotFoundInStorageError{ObjectKey: doc.ObjectKey}
	}

	// Verify the stored object matches what was declared at initiate
	if verr := s.verifyStoredObject(doc, info); verr != nil {
		log.Printf("Stored object failed verification for document %s: %v", req.DocumentID, verr)
		s.failUploadVerification(ctx, req.DocumentID, verr)
		return nil, verr
	}

	// ========== Step 6: Begin transaction for atomic state update + event creation ==========
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// ========== Step 7: Perform state transition with validation ==========
	if err := s.repo.TransitionInitiatedToUploaded(ctx, tx, req.DocumentID); err != nil {
		// State transition failed - likely concurrent update
		log.Printf("State transition failed for document %s: %v", req.DocumentID, err)
		return nil, fmt.Errorf("state transition failed: %w", err)
	}

	// ========== Step 8: Create outbox event ==========
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
		return nil, fmt.Errorf("failed to convert event payload: %w", err)
	}

	payloadJSON, err := json.Marshal(payloadMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal event payload: %w", err)
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
		return nil, fmt.Errorf("failed to create outbox event: %w", err)
	}

	// ========== Step 9: Commit transaction atomically ==========
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Printf("Document %s successfully completed upload. Event ID: %s", req.DocumentID, eventID)

	return &dto.CompleteUploadResponse{
		DocumentID: req.DocumentID,
		Status:     string(model.StatusUploaded),
	}, nil
}

func (s *documentService) FinalizeUpload(ctx context.Context, documentID string) error {
	// ========== Step 1: Fetch document ==========
	doc, err := s.repo.GetByID(ctx, documentID)
	if err != nil {
		return fmt.Errorf("failed to fetch document: %w", err)
	}
	if doc == nil {
		return &model.DocumentNotFoundError{DocumentID: documentID}
	}

	// ========== Step 2: Handle idempotency - if already UPLOADED, no action needed ==========
	if doc.Status == model.StatusUploaded {
		log.Printf("Document %s already in UPLOADED state (recovery path, no event created)", documentID)
		return nil
	}

	// ========== Step 3: Validate state is INITIATED (recovery only works from this state) ==========
	if doc.Status != model.StatusInitiated {
		return &model.InvalidStateTransitionError{
			DocumentID:      documentID,
			CurrentStatus:   doc.Status,
			AttemptedStatus: model.StatusUploaded,
		}
	}

	// ========== Step 4: Verify file exists in storage ==========
	log.Printf("Recovery path: Verifying file exists in storage for document %s (object key: %s)", documentID, doc.ObjectKey)
	info, err := s.storage.InspectObject(ctx, doc.ObjectKey, objectHeadBytes)
	if err != nil {
		return fmt.Errorf("failed to verify file in storage: %w", err)
	}

	if info == nil {
		// File not in storage - mark as failed and return
		log.Printf("File not found in storage during recovery for document %s, marking as FAILED", documentID)
		changed, failErr := s.repo.TransitionInitiatedToFailed(ctx, documentID, model.FailureStageUpload, "file not found in storage during recovery")
		if failErr != nil {
			log.Printf("Failed to mark document %s as FAILED: %v", documentID, failErr)
		} else if !changed {
			log.Printf("Document %s changed state concurrently; not marking FAILED", documentID)
		}
		return &model.FileNotFoundInStorageError{ObjectKey: doc.ObjectKey}
	}

	// Verify the stored object matches what was declared at initiate
	if verr := s.verifyStoredObject(doc, info); verr != nil {
		log.Printf("Stored object failed verification during recovery for document %s: %v", documentID, verr)
		s.failUploadVerification(ctx, documentID, verr)
		return verr
	}

	// ========== Step 5: Begin transaction ==========
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// ========== Step 6: Perform state transition with validation ==========
	changed, err := s.repo.EnsureUploadedStatus(ctx, tx, documentID)
	if err != nil {
		log.Printf("State transition failed for document %s: %v", documentID, err)
		return fmt.Errorf("state transition failed: %w", err)
	}

	// ========== Step 7: Create outbox event ONLY if we actually changed the state ==========
	if !changed {
		// State was already UPLOADED - this shouldn't happen after our check above
		// but it can if concurrent request beat us to it
		log.Printf("Document %s was already transitioned to UPLOADED (concurrent update)", documentID)
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("failed to commit transaction: %w", err)
		}
		return nil
	}

	// Create event only if we successfully transitioned state
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
		return fmt.Errorf("failed to convert event payload: %w", err)
	}

	payloadJSON, err := json.Marshal(payloadMap)
	if err != nil {
		return fmt.Errorf("failed to marshal event payload: %w", err)
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
		return fmt.Errorf("failed to create outbox event: %w", err)
	}

	// ========== Step 8: Commit transaction atomically ==========
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Printf("Document %s successfully recovered via finalize upload. Event ID: %s", documentID, eventID)

	return nil
}

func (s *documentService) MarkDocumentFailed(ctx context.Context, documentID string, userID string, failureStage model.FailureStage, failureReason string) error {
	if failureStage != model.FailureStageUpload && failureStage != model.FailureStageProcessing {
		return &model.InvalidFailureStageError{Stage: failureStage}
	}
	if err := validateIdentifier("document_id", documentID); err != nil {
		return err
	}
	if err := validateIdentifier("user_id", userID); err != nil {
		return err
	}

	// ========== Step 1: Fetch document ==========
	doc, err := s.repo.GetByID(ctx, documentID)
	if err != nil {
		return fmt.Errorf("failed to fetch document: %w", err)
	}
	if doc == nil {
		return &model.DocumentNotFoundError{DocumentID: documentID}
	}
	if doc.UserID != userID {
		return &model.UnauthorizedDocumentError{DocumentID: documentID}
	}

	log.Printf("Marking document %s as FAILED at stage %s: %s", documentID, failureStage, failureReason)

	// ========== Step 2: Begin transaction ==========
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// ========== Step 3: Perform state transition ==========
	if err := s.repo.TransitionAnyToFailed(ctx, tx, documentID, failureStage, failureReason); err != nil {
		log.Printf("Failed to transition document %s to FAILED state: %v", documentID, err)
		return fmt.Errorf("state transition failed: %w", err)
	}

	// ========== Step 4: Commit transaction ==========
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Printf("Document %s successfully marked as FAILED", documentID)
	return nil
}

func (s *documentService) GetDocument(ctx context.Context, req dto.GetDocumentRequest) (*dto.GetDocumentResponse, error) {
	if err := validateIdentifier("document_id", req.DocumentID); err != nil {
		return nil, err
	}
	if err := validateIdentifier("user_id", req.UserID); err != nil {
		return nil, err
	}

	doc, err := s.repo.GetByID(ctx, req.DocumentID)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, &model.DocumentNotFoundError{DocumentID: req.DocumentID}
	}
	if doc.UserID != req.UserID {
		return nil, &model.UnauthorizedDocumentError{DocumentID: req.DocumentID}
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

const (
	maxKeyFileNameLen   = 100
	maxKeyExtLen        = 10
	fallbackKeyFileName = "file"
)

func generateObjectKey(userID, documentID, fileName string) string {
	return fmt.Sprintf("%s/%s/%s", userID, documentID, sanitizeFileNameForKey(fileName))
}

// sanitizeFileNameForKey turns a client-supplied file name into a safe final key segment.
// The original name is still stored unchanged in the database; only the object key uses this.
func sanitizeFileNameForKey(name string) string {
	// Step 1: keep only the last path segment (drops "../" and "dir/" prefixes, both separators)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}

	// Step 2: allow only [A-Za-z0-9._-]; everything else (spaces, #, ?, control chars, non-ASCII) becomes "_"
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}

	// Step 3: no leading dots (no "." / ".." / hidden names); fall back if nothing is left
	safe := strings.TrimLeft(b.String(), ".")
	if safe == "" {
		return fallbackKeyFileName
	}

	// Step 4: cap the length, preserving a short extension
	if len(safe) > maxKeyFileNameLen {
		ext := path.Ext(safe)
		if len(ext) > maxKeyExtLen {
			ext = ""
		}
		safe = safe[:maxKeyFileNameLen-len(ext)] + ext
	}
	return safe
}

func validateIdentifier(field, value string) error {
	if _, err := uuid.Parse(value); err != nil {
		return &model.InvalidIdentifierError{Field: field, Value: value}
	}
	return nil
}

// verifyStoredObject checks the real stored size and file header against what was declared.
func (s *documentService) verifyStoredObject(doc *model.Document, info *storage.ObjectInfo) error {
	if info.Size > s.config.MAX_SIZE {
		return &model.FileSizeExceededError{Size: info.Size, MaxSize: s.config.MAX_SIZE}
	}
	if info.Size != doc.Size {
		return &model.FileSizeMismatchError{Declared: doc.Size, Actual: info.Size}
	}

	switch doc.ContentType {
	case "application/pdf":
		// The PDF spec allows a short preamble before the header, so search the head.
		if !bytes.Contains(info.Head, []byte("%PDF-")) {
			return &model.FileContentMismatchError{ContentType: doc.ContentType}
		}
	case "image/jpeg":
		if !bytes.HasPrefix(info.Head, []byte{0xFF, 0xD8, 0xFF}) {
			return &model.FileContentMismatchError{ContentType: doc.ContentType}
		}
	}
	return nil
}

// failUploadVerification marks an INITIATED document FAILED (guarded) after a verification failure.
// Errors and lost races are logged only; the caller still returns the verification error.
func (s *documentService) failUploadVerification(ctx context.Context, documentID string, verr error) {
	changed, failErr := s.repo.TransitionInitiatedToFailed(ctx, documentID, model.FailureStageUpload, verr.Error())
	if failErr != nil {
		log.Printf("Failed to mark document %s as FAILED: %v", documentID, failErr)
	} else if !changed {
		log.Printf("Document %s changed state concurrently; not marking FAILED", documentID)
	}
}
