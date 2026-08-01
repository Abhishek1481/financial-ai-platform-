package ingestion

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	commonv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/common/v1"
	ingestionv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/ingestion/v1"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/storage"
)

// fakeExtractor stands in for a live ml-service connection — Extractor is
// an interface specifically so this doesn't need one (see extractor.go).
type fakeExtractor struct {
	mu     sync.Mutex
	calls  int
	respFn func(uri string) (*ingestionv1.ExtractDocumentResponse, error)
}

func (f *fakeExtractor) ExtractDocument(
	ctx context.Context,
	documentID, uri string,
	docType commonv1.DocumentType,
) (*ingestionv1.ExtractDocumentResponse, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.respFn != nil {
		return f.respFn(uri)
	}
	return &ingestionv1.ExtractDocumentResponse{RawText: "extracted text", PageCount: 1}, nil
}

func (f *fakeExtractor) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newTestService(t *testing.T, ml Extractor) *Service {
	t.Helper()
	store, err := storage.NewLocalObjectStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalObjectStore() error: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(logger, NewMemoryDocumentRepository(), NewMemoryJobRepository(), store, ml, 2, 10)

	ctx, cancel := context.WithCancel(context.Background())
	svc.Start(ctx)
	t.Cleanup(cancel)

	return svc
}

// waitForJobStatus polls rather than synchronizing on a channel because
// job processing genuinely happens asynchronously on the worker pool,
// same as it would against a real ml-service — the test has to observe
// that from the outside, the same way an HTTP client polling
// GET /api/v1/documents/:id would.
func waitForJobStatus(t *testing.T, svc *Service, documentID string, want JobStatus) Job {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job, err := svc.GetLatestJob(context.Background(), documentID)
		if err == nil && job.Status == want {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job for document %s did not reach status %s in time", documentID, want)
	return Job{}
}

func TestService_UploadUnsupportedExtensionFails(t *testing.T) {
	svc := newTestService(t, &fakeExtractor{})

	_, err := svc.Upload(context.Background(), "user-1", "doc.exe", CategoryGeneral, strings.NewReader("data"))
	if !errors.Is(err, ErrUnsupportedFileType) {
		t.Errorf("error = %v, want %v", err, ErrUnsupportedFileType)
	}
}

func TestService_UploadAndExtractionCompletes(t *testing.T) {
	ml := &fakeExtractor{}
	svc := newTestService(t, ml)

	result, err := svc.Upload(
		context.Background(), "user-1", "report.txt", CategoryGeneral, strings.NewReader("Q1 revenue grew"),
	)
	if err != nil {
		t.Fatalf("Upload() error: %v", err)
	}
	if result.Job.Status != JobStatusPending {
		t.Errorf("initial job status = %s, want %s", result.Job.Status, JobStatusPending)
	}

	job := waitForJobStatus(t, svc, result.Document.ID, JobStatusCompleted)
	if job.ExtractedText != "extracted text" {
		t.Errorf("ExtractedText = %q, want %q", job.ExtractedText, "extracted text")
	}
	if ml.callCount() != 1 {
		t.Errorf("extractor called %d times, want 1", ml.callCount())
	}
}

func TestService_DuplicateUploadReusesDocumentAndDoesNotReExtract(t *testing.T) {
	ml := &fakeExtractor{}
	svc := newTestService(t, ml)

	content := "identical content"
	first, err := svc.Upload(context.Background(), "user-1", "a.txt", CategoryGeneral, strings.NewReader(content))
	if err != nil {
		t.Fatalf("first Upload() error: %v", err)
	}
	waitForJobStatus(t, svc, first.Document.ID, JobStatusCompleted)

	second, err := svc.Upload(context.Background(), "user-1", "b.txt", CategoryGeneral, strings.NewReader(content))
	if err != nil {
		t.Fatalf("second Upload() error: %v", err)
	}
	if !second.Reused {
		t.Error("second Upload() with identical content: Reused = false, want true")
	}
	if second.Document.ID != first.Document.ID {
		t.Errorf("second Document.ID = %s, want %s (same document)", second.Document.ID, first.Document.ID)
	}
	if ml.callCount() != 1 {
		t.Errorf("extractor called %d times after duplicate upload, want 1 (no re-extraction)", ml.callCount())
	}
}

func TestService_ExtractionFailureMarksJobFailed(t *testing.T) {
	ml := &fakeExtractor{respFn: func(uri string) (*ingestionv1.ExtractDocumentResponse, error) {
		return nil, errors.New("ml-service unreachable")
	}}
	svc := newTestService(t, ml)

	result, err := svc.Upload(context.Background(), "user-1", "doc.txt", CategoryGeneral, strings.NewReader("content"))
	if err != nil {
		t.Fatalf("Upload() error: %v", err)
	}

	job := waitForJobStatus(t, svc, result.Document.ID, JobStatusFailed)
	if job.Error == "" {
		t.Error("failed job has empty Error field")
	}
}

func TestService_SECFilingCategoryRequiresHTMLOrTXT(t *testing.T) {
	svc := newTestService(t, &fakeExtractor{})

	_, err := svc.Upload(context.Background(), "user-1", "filing.pdf", CategorySECFiling, strings.NewReader("data"))
	if err == nil {
		t.Error("Upload() with sec_filing category and .pdf: expected error, got nil")
	}
}

func TestService_GetDocumentNotFound(t *testing.T) {
	svc := newTestService(t, &fakeExtractor{})

	_, err := svc.GetDocument(context.Background(), "does-not-exist")
	if !errors.Is(err, ErrDocumentNotFound) {
		t.Errorf("error = %v, want %v", err, ErrDocumentNotFound)
	}
}
