package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/auth"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/ingestion"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/worker"
)

// DocumentHandlers is the HTTP transport layer over ingestion.Service —
// same split as AuthHandlers/auth.Service: request parsing and status-code
// mapping live here, upload/dedup/job-queueing logic lives in the domain
// package, testable without an HTTP server.
type DocumentHandlers struct {
	service        *ingestion.Service
	maxUploadBytes int64
}

func NewDocumentHandlers(service *ingestion.Service, maxUploadBytes int64) *DocumentHandlers {
	return &DocumentHandlers{service: service, maxUploadBytes: maxUploadBytes}
}

type uploadResponse struct {
	DocumentID string `json:"document_id"`
	JobID      string `json:"job_id"`
	Filename   string `json:"filename"`
	Status     string `json:"status"`
	Reused     bool   `json:"reused,omitempty"`
}

func (h *DocumentHandlers) Upload(c *gin.Context) {
	// Rejects an oversized body while it's still streaming in, rather than
	// buffering it fully first and finding out too late — MaxBytesReader
	// makes the next Read past the limit fail instead of succeeding.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.maxUploadBytes)

	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing or invalid 'file' form field"})
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "could not read uploaded file"})
		return
	}
	defer file.Close()

	category := ingestion.Category(c.DefaultPostForm("category", string(ingestion.CategoryGeneral)))

	claims, ok := auth.CurrentClaims(c)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no authenticated claims on request"})
		return
	}

	result, err := h.service.Upload(c.Request.Context(), claims.UserID, fileHeader.Filename, category, file)
	if err != nil {
		writeUploadError(c, err)
		return
	}

	status := http.StatusAccepted
	if result.Reused {
		// A duplicate upload (same content hash) reuses the existing
		// document instead of re-queueing extraction — 200, not 201,
		// since nothing new was created.
		status = http.StatusOK
	}
	c.JSON(status, uploadResponse{
		DocumentID: result.Document.ID,
		JobID:      result.Job.ID,
		Filename:   result.Document.Filename,
		Status:     string(result.Job.Status),
		Reused:     result.Reused,
	})
}

func writeUploadError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ingestion.ErrUnsupportedFileType):
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": err.Error()})
	case errors.Is(err, worker.ErrQueueFull):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "ingestion queue is full, try again shortly"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

// extractedTextPreviewChars bounds the response body size for a
// completed job — the full text is what the vector store consumes
// (chunked, embedded, and upserted server-side), not something an API
// client polling status needs in full.
const extractedTextPreviewChars = 500

type inferredMetadataView struct {
	Ticker       string `json:"ticker,omitempty"`
	CompanyName  string `json:"company_name,omitempty"`
	FilingType   string `json:"filing_type,omitempty"`
	FiscalPeriod string `json:"fiscal_period,omitempty"`
}

type jobStatusView struct {
	Status                 string                `json:"status"`
	Error                  string                `json:"error,omitempty"`
	ExtractedTextPreview   string                `json:"extracted_text_preview,omitempty"`
	ExtractedTextLength    int                   `json:"extracted_text_length,omitempty"`
	TableCount             int                   `json:"table_count,omitempty"`
	PageCount              int                   `json:"page_count,omitempty"`
	Metadata               *inferredMetadataView `json:"metadata,omitempty"`
	ChunkCount             int                   `json:"chunk_count,omitempty"`
	ChunksSkippedDuplicate int                   `json:"chunks_skipped_duplicate,omitempty"`
}

type documentResponse struct {
	ID         string        `json:"id"`
	Filename   string        `json:"filename"`
	SizeBytes  int64         `json:"size_bytes"`
	UploadedBy string        `json:"uploaded_by"`
	CreatedAt  string        `json:"created_at"`
	Job        jobStatusView `json:"job"`
}

func (h *DocumentHandlers) GetDocument(c *gin.Context) {
	id := c.Param("id")

	doc, err := h.service.GetDocument(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "document not found"})
		return
	}

	job, err := h.service.GetLatestJob(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found for document"})
		return
	}

	preview := job.ExtractedText
	if len(preview) > extractedTextPreviewChars {
		preview = preview[:extractedTextPreviewChars]
	}

	c.JSON(http.StatusOK, documentResponse{
		ID:         doc.ID,
		Filename:   doc.Filename,
		SizeBytes:  doc.SizeBytes,
		UploadedBy: doc.UploadedBy,
		CreatedAt:  doc.CreatedAt.Format(time.RFC3339),
		Job: jobStatusView{
			Status:                 string(job.Status),
			Error:                  job.Error,
			ExtractedTextPreview:   preview,
			ExtractedTextLength:    len(job.ExtractedText),
			TableCount:             job.TableCount,
			PageCount:              job.PageCount,
			Metadata:               metadataView(job.Metadata),
			ChunkCount:             job.ChunkCount,
			ChunksSkippedDuplicate: job.ChunksSkippedDuplicate,
		},
	})
}

// metadataView returns nil (omitted from the JSON body entirely, via
// jobStatusView's omitempty) when every field is blank — extraction
// outside the SEC_FILING category never populates any of them, and an
// empty-but-present "metadata": {} would misleadingly suggest inference
// ran and found nothing, rather than never having run at all.
func metadataView(m ingestion.InferredMetadata) *inferredMetadataView {
	if m == (ingestion.InferredMetadata{}) {
		return nil
	}
	return &inferredMetadataView{
		Ticker:       m.Ticker,
		CompanyName:  m.CompanyName,
		FilingType:   m.FilingType,
		FiscalPeriod: m.FiscalPeriod,
	}
}
