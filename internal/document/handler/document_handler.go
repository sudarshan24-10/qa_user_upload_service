package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sudarshan24-10/qa_user_upload_service/internal/document/dto"
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, res)
}

// -------------------- FAIL --------------------

func (h *UploadHandler) FailUpload(c *gin.Context) {
	var req dto.FailedUploadRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := h.service.FailUpload(c.Request.Context(), req.DocumentID, req.UserID, req.IdempotencyKey, req.FailedReason)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "upload marked as failed",
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

	err := h.service.FinilizeUpload(c.Request.Context(), req.DocumentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "upload finalized successfully",
	})
}

// -------------------- GET DOCUMENT --------------------

func (h *UploadHandler) GetDocument(c *gin.Context) {
	var req dto.GetDocumentRequest

	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	res, err := h.service.GetDocument(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, res)
}
