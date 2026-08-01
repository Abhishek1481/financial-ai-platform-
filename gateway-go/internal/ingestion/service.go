// Package ingestion is the domain layer for document upload and
// extraction: it owns Document/Job records, dedup, and handing work off to
// the worker pool that calls ml-service. HTTP handlers translate requests
// to calls here; this package never imports gin.
package ingestion

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/storage"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/worker"
	commonv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/common/v1"
)

// Category distinguishes an ordinary upload from one the caller already
// knows is an SEC filing — see doctype.go's comment on why that can't be
// inferred from bytes alone.
type Category string

const (
	CategoryGeneral   Category = "general"
	CategorySECFiling Category = "sec_filing"
)

// Independent of (and larger than) the HTTP-layer max-upload-size check in
// handlers/documents.go — this is a hard sanity ceiling on how much this
// package will ever buffer in memory for one upload, regardless of what
// the HTTP config allows.
const maxUploadBufferBytes = 64 << 20 // 64 MiB

const extractionTimeout = 2 * time.Minute

type Service struct {
	logger *slog.Logger
	docs   DocumentRepository
	jobs   JobRepository
	store  storage.ObjectStore
	ml     Extractor
	pool   *worker.Pool[Job]
}

func NewService(
	logger *slog.Logger,
	docs DocumentRepository,
	jobs JobRepository,
	store storage.ObjectStore,
	ml Extractor,
	workers, queueSize int,
) *Service {
	s := &Service{logger: logger, docs: docs, jobs: jobs, store: store, ml: ml}
	s.pool = worker.NewPool(workers, queueSize, s.processJob)
	return s
}

// Start runs the worker pool against ctx — a long-lived, app-lifetime
// context, not a per-request one: extraction happens after the HTTP
// request that triggered it has already returned 202.
func (s *Service) Start(ctx context.Context) {
	s.pool.Start(ctx)
}

func (s *Service) Stop(ctx context.Context) error {
	return s.pool.Stop(ctx)
}

// UploadResult reports both what got created/reused and whether this was
// actually a new upload, so the HTTP handler can pick 201 vs 200.
type UploadResult struct {
	Document Document
	Job      Job
	Reused   bool
}

func (s *Service) Upload(
	ctx context.Context,
	uploaderID, filename string,
	category Category,
	r io.Reader,
) (UploadResult, error) {
	docType, err := docTypeForFilename(filename)
	if err != nil {
		return UploadResult{}, err
	}
	if category == CategorySECFiling {
		if docType != commonv1.DocumentType_DOCUMENT_TYPE_HTML &&
			docType != commonv1.DocumentType_DOCUMENT_TYPE_TXT {
			return UploadResult{}, fmt.Errorf(
				"%w: sec_filing category requires .html/.htm/.txt, got %s",
				ErrUnsupportedFileType, filename,
			)
		}
		docType = commonv1.DocumentType_DOCUMENT_TYPE_SEC_FILING
	}

	// Buffered fully in memory (bounded by both this cap and the HTTP
	// layer's own max-upload-size check) rather than streamed straight to
	// storage: dedup needs the complete content hash before it can even
	// check for a duplicate, and re-reading a temp file back out for
	// hashing would just move the same cost elsewhere for no benefit at
	// this size range.
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(r, maxUploadBufferBytes+1))
	if err != nil {
		return UploadResult{}, fmt.Errorf("ingestion: read upload: %w", err)
	}
	if n > maxUploadBufferBytes {
		return UploadResult{}, fmt.Errorf("ingestion: upload exceeds %d bytes", maxUploadBufferBytes)
	}

	hash := sha256.Sum256(buf.Bytes())
	contentHash := hex.EncodeToString(hash[:])

	existing, err := s.docs.FindByContentHash(ctx, contentHash)
	switch {
	case err == nil:
		job, jerr := s.jobs.FindLatestByDocumentID(ctx, existing.ID)
		if jerr != nil {
			return UploadResult{}, fmt.Errorf("ingestion: find job for duplicate document: %w", jerr)
		}
		return UploadResult{Document: existing, Job: job, Reused: true}, nil
	case errors.Is(err, ErrDocumentNotFound):
		// not a duplicate — fall through to create a new document
	default:
		return UploadResult{}, fmt.Errorf("ingestion: check for duplicate: %w", err)
	}

	docID := uuid.NewString()
	uri, err := s.store.Put(ctx, docID+"/"+filename, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return UploadResult{}, fmt.Errorf("ingestion: store upload: %w", err)
	}

	now := time.Now()
	doc := Document{
		ID:          docID,
		Filename:    filename,
		DocType:     docType,
		StorageURI:  uri,
		ContentHash: contentHash,
		SizeBytes:   n,
		UploadedBy:  uploaderID,
		CreatedAt:   now,
	}
	if err := s.docs.Create(ctx, doc); err != nil {
		return UploadResult{}, fmt.Errorf("ingestion: create document record: %w", err)
	}

	job := Job{
		ID:         uuid.NewString(),
		DocumentID: docID,
		Status:     JobStatusPending,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.jobs.Create(ctx, job); err != nil {
		return UploadResult{}, fmt.Errorf("ingestion: create job record: %w", err)
	}

	if err := s.pool.Submit(job); err != nil {
		return UploadResult{}, fmt.Errorf("ingestion: %w", err)
	}

	return UploadResult{Document: doc, Job: job}, nil
}

func (s *Service) GetDocument(ctx context.Context, id string) (Document, error) {
	return s.docs.FindByID(ctx, id)
}

func (s *Service) GetLatestJob(ctx context.Context, documentID string) (Job, error) {
	return s.jobs.FindLatestByDocumentID(ctx, documentID)
}

// processJob is the worker pool's ProcessFunc — it runs on the pool's
// background context, well after the HTTP request that created the job
// has already returned.
func (s *Service) processJob(ctx context.Context, job Job) {
	job.Status = JobStatusProcessing
	job.UpdatedAt = time.Now()
	if err := s.jobs.Update(ctx, job); err != nil {
		s.logger.Error("failed to mark job processing", "job_id", job.ID, "error", err)
	}

	doc, err := s.docs.FindByID(ctx, job.DocumentID)
	if err != nil {
		s.failJob(ctx, job, fmt.Errorf("load document: %w", err))
		return
	}

	extractCtx, cancel := context.WithTimeout(ctx, extractionTimeout)
	defer cancel()

	resp, err := s.ml.ExtractDocument(extractCtx, doc.ID, doc.StorageURI, doc.DocType)
	if err != nil {
		s.failJob(ctx, job, err)
		return
	}

	now := time.Now()
	job.Status = JobStatusCompleted
	job.ExtractedText = resp.GetRawText()
	job.TableCount = len(resp.GetTables())
	job.PageCount = int(resp.GetPageCount())
	if md := resp.GetInferredMetadata(); md != nil {
		job.Metadata = InferredMetadata{
			Ticker:       md.GetTicker(),
			CompanyName:  md.GetCompanyName(),
			FilingType:   md.GetFilingType(),
			FiscalPeriod: md.GetFiscalPeriod(),
		}
	}
	job.UpdatedAt = now
	job.CompletedAt = &now
	if err := s.jobs.Update(ctx, job); err != nil {
		s.logger.Error("failed to mark job completed", "job_id", job.ID, "error", err)
	}

	s.logger.Info("document extraction completed",
		"job_id", job.ID,
		"document_id", doc.ID,
		"text_length", len(job.ExtractedText),
		"table_count", job.TableCount,
	)
}

func (s *Service) failJob(ctx context.Context, job Job, cause error) {
	now := time.Now()
	job.Status = JobStatusFailed
	job.Error = cause.Error()
	job.UpdatedAt = now
	job.CompletedAt = &now
	if err := s.jobs.Update(ctx, job); err != nil {
		s.logger.Error("failed to mark job failed", "job_id", job.ID, "error", err)
	}
	s.logger.Warn("document extraction failed",
		"job_id", job.ID, "document_id", job.DocumentID, "error", cause,
	)
}
