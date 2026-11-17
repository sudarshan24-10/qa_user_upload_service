package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/dto"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/model"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/service"
)

type UploadHandler struct {
	service service.DocumentService
}

func NewUploadHandler(service service.DocumentService) *UploadHandler {
	return &UploadHandler{service: service}
}

// -------------------- INITIATE --------------------

func (h *UploadHandler) InitiateUpload(c *gin.Context) {
	var req dto.InitiateUploadRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	res, err := h.service.InitiateUpload(c.Request.Context(), req)
	if err != nil {
		c.JSON(httpStatusFromError(err), gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, res)
}

// -------------------- COMPLETE --------------------

func (h *UploadHandler) CompleteUpload(c *gin.Context) {
	var req dto.CompleteUploadRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	res, err := h.service.CompleteUpload(c.Request.Context(), req)
	if err != nil {
		c.JSON(httpStatusFromError(err), gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, res)
}

// -------------------- MARK FAILED --------------------

func (h *UploadHandler) MarkDocumentFailed(c *gin.Context) {
	var req dto.MarkDocumentFailedRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := h.service.MarkDocumentFailed(
		c.Request.Context(),
		req.DocumentID,
		req.UserID,
		model.FailureStage(req.FailureStage),
		req.FailureReason,
	)
	if err != nil {
		c.JSON(httpStatusFromError(err), gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "document marked as failed",
	})
}

// -------------------- FINALIZE --------------------

func (h *UploadHandler) FinalizeUpload(c *gin.Context) {
	var req struct {
		DocumentID string `json:"document_id" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := h.service.FinalizeUpload(c.Request.Context(), req.DocumentID)
	if err != nil {
		c.JSON(httpStatusFromError(err), gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "upload finalized successfully",
	})
}

// -------------------- GET DOCUMENT --------------------

func (h *UploadHandler) GetDocument(c *gin.Context) {
	var query struct {
		UserID string `form:"user_id" binding:"required"`
	}

	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	req := dto.GetDocumentRequest{
		DocumentID: c.Param("id"),
		UserID:     query.UserID,
	}

	res, err := h.service.GetDocument(c.Request.Context(), req)
	if err != nil {
		c.JSON(httpStatusFromError(err), gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, res)
}

// httpStatusFromError maps domain errors to their correct HTTP status codes.
// Uses errors.As to correctly handle wrapped errors.
func httpStatusFromError(err error) int {
	var notFound *model.DocumentNotFoundError
	if errors.As(err, &notFound) {
		return http.StatusNotFound
	}

	var unauthorized *model.UnauthorizedDocumentError
	if errors.As(err, &unauthorized) {
		return http.StatusForbidden
	}

	var idempotencyMismatch *model.IdempotencyKeyMismatchError
	if errors.As(err, &idempotencyMismatch) {
		return http.StatusConflict
	}

	var idempotencyReuse *model.IdempotencyKeyReuseError
	if errors.As(err, &idempotencyReuse) {
		return http.StatusConflict
	}

	var invalidFailureStage *model.InvalidFailureStageError
	if errors.As(err, &invalidFailureStage) {
		return http.StatusBadRequest
	}

	var unsupportedContentType *model.UnsupportedContentTypeError
	if errors.As(err, &unsupportedContentType) {
		return http.StatusBadRequest
	}

	var fileSizeExceeded *model.FileSizeExceededError
	if errors.As(err, &fileSizeExceeded) {
		return http.StatusBadRequest
	}

	var invalidIdentifier *model.InvalidIdentifierError
	if errors.As(err, &invalidIdentifier) {
		return http.StatusBadRequest
	}

	var validationErrors validator.ValidationErrors
	if errors.As(err, &validationErrors) {
		return http.StatusBadRequest
	}

	var conflict *model.InvalidStateTransitionError
	if errors.As(err, &conflict) {
		return http.StatusConflict
	}

	var sizeMismatch *model.FileSizeMismatchError
	if errors.As(err, &sizeMismatch) {
		return http.StatusUnprocessableEntity
	}

	var contentMismatch *model.FileContentMismatchError
	if errors.As(err, &contentMismatch) {
		return http.StatusUnprocessableEntity
	}

	var unprocessable *model.FileNotFoundInStorageError
	if errors.As(err, &unprocessable) {
		return http.StatusUnprocessableEntity
	}

	return http.StatusInternalServerError
}
