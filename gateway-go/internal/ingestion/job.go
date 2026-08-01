package ingestion

import "time"

type JobStatus string

const (
	JobStatusPending    JobStatus = "pending"
	JobStatusProcessing JobStatus = "processing"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusFailed     JobStatus = "failed"
)

// InferredMetadata is best-effort metadata ml-service pulled from the
// document's own content during extraction (see
// ml-service/app/extraction/sec_metadata.py) — never authoritative, and
// frequently empty outside the SEC_FILING category.
type InferredMetadata struct {
	Ticker       string
	CompanyName  string
	FilingType   string
	FiscalPeriod string
}

// Job tracks one attempt at extracting a Document. Kept separate from
// Document (rather than a single "status" field on it) because a document
// can be reprocessed — a failed extraction retried, or re-run after a
// SEC-category correction — and each attempt deserves its own status and
// error history instead of overwriting the last one.
type Job struct {
	ID         string
	DocumentID string
	Status     JobStatus
	Error      string

	// Populated once Status == JobStatusCompleted.
	ExtractedText string
	TableCount    int
	PageCount     int
	Metadata      InferredMetadata

	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time
}
