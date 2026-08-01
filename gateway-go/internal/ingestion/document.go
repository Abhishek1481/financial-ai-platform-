package ingestion

import (
	"time"

	commonv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/common/v1"
)

// Document is the record of an uploaded file — immutable once created.
// Re-processing (Phase 7's re-embedding, say) creates a new Job against
// the same Document rather than mutating this struct.
type Document struct {
	ID          string
	Filename    string
	DocType     commonv1.DocumentType
	StorageURI  string
	ContentHash string // sha256 of the raw bytes, used for dedup
	SizeBytes   int64
	UploadedBy  string // user ID from the authenticated request
	CreatedAt   time.Time
}
