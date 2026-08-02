package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/auth"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/evaluation"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/ingestion"
)

// AdminPing exists to prove the RBAC wiring (Authenticate + RequireRole)
// actually gates a route end-to-end.
func AdminPing(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": "pong"})
}

// AdminHandlers is the admin dashboard's HTTP transport layer (Phase 15):
// read-only visibility over documents/jobs/users/aggregate stats, plus
// the answer-quality spot-check from Phase 12. It composes the same
// Service types the public handlers use rather than owning any state of
// its own — an admin sees the same data a regular user's requests
// produce, just across every user instead of scoped to one.
type AdminHandlers struct {
	evaluator evaluation.Evaluator
	authSvc   *auth.Service
	ingestSvc *ingestion.Service
}

func NewAdminHandlers(
	evaluator evaluation.Evaluator, authSvc *auth.Service, ingestSvc *ingestion.Service,
) *AdminHandlers {
	return &AdminHandlers{evaluator: evaluator, authSvc: authSvc, ingestSvc: ingestSvc}
}

type evaluateAnswerBody struct {
	Question          string   `json:"question" binding:"required"`
	Answer            string   `json:"answer" binding:"required"`
	Context           []string `json:"context"`
	GroundTruthAnswer string   `json:"ground_truth_answer"`
}

type evaluateAnswerResponseView struct {
	Faithfulness       float32 `json:"faithfulness"`
	ContextPrecision   float32 `json:"context_precision"`
	ContextRecall      float32 `json:"context_recall"`
	HallucinationScore float32 `json:"hallucination_score"`
	AnswerRelevancy    float32 `json:"answer_relevancy"`
}

// EvaluateAnswer handles POST /api/v1/admin/evaluate — an admin-only,
// on-demand spot-check of a single question/answer/context triple against
// EvaluationService.EvaluateAnswer. Batch evaluation (BatchEvaluate,
// client-streaming) is a CI eval-regression gate, not a user-facing HTTP
// concern — it lands with the CI pipeline in Phase 19, driven directly over
// gRPC rather than through gateway-go.
func (h *AdminHandlers) EvaluateAnswer(c *gin.Context) {
	var body evaluateAnswerBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.evaluator.EvaluateAnswer(
		c.Request.Context(), body.Question, body.Answer, body.Context, body.GroundTruthAnswer,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "evaluate failed"})
		return
	}

	c.JSON(http.StatusOK, evaluateAnswerResponseView{
		Faithfulness:       resp.GetFaithfulness(),
		ContextPrecision:   resp.GetContextPrecision(),
		ContextRecall:      resp.GetContextRecall(),
		HallucinationScore: resp.GetHallucinationScore(),
		AnswerRelevancy:    resp.GetAnswerRelevancy(),
	})
}

type userView struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
}

// ListUsers handles GET /api/v1/admin/users. Deliberately never includes
// PasswordHash — userView has no field for it, so there's no risk of a
// future refactor accidentally serializing it the way there would be if
// this reused auth.User directly with a json struct tag to hide it.
func (h *AdminHandlers) ListUsers(c *gin.Context) {
	users, err := h.authSvc.ListUsers(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list users"})
		return
	}

	views := make([]userView, 0, len(users))
	for _, u := range users {
		views = append(views, userView{
			ID:        u.ID,
			Email:     u.Email,
			Role:      string(u.Role),
			CreatedAt: u.CreatedAt.Format(time.RFC3339),
		})
	}
	c.JSON(http.StatusOK, gin.H{"users": views})
}

// ListDocuments handles GET /api/v1/admin/documents — every document
// across every user, each paired with its latest job's status (same
// jobStatusView shape GetDocument returns for one document). A document
// whose job somehow can't be found (shouldn't happen — Upload always
// creates one — but ListAll's no-pagination, best-effort nature means
// this is defensive) is included with a zero-value job status rather than
// dropped, so the listing's document count always matches ListAll's.
func (h *AdminHandlers) ListDocuments(c *gin.Context) {
	docs, err := h.ingestSvc.ListDocuments(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list documents"})
		return
	}

	views := make([]documentResponse, 0, len(docs))
	for _, doc := range docs {
		job, _ := h.ingestSvc.GetLatestJob(c.Request.Context(), doc.ID)
		preview := job.ExtractedText
		if len(preview) > extractedTextPreviewChars {
			preview = preview[:extractedTextPreviewChars]
		}
		views = append(views, documentResponse{
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
	c.JSON(http.StatusOK, gin.H{"documents": views})
}

type jobView struct {
	ID         string `json:"id"`
	DocumentID string `json:"document_id"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	ChunkCount int    `json:"chunk_count,omitempty"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// ListJobs handles GET /api/v1/admin/jobs — every processing attempt for
// every document (not just the latest per document, unlike
// ListDocuments's embedded job), so a failed-then-retried document shows
// its full history.
func (h *AdminHandlers) ListJobs(c *gin.Context) {
	jobs, err := h.ingestSvc.ListJobs(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list jobs"})
		return
	}

	views := make([]jobView, 0, len(jobs))
	for _, job := range jobs {
		views = append(views, jobView{
			ID:         job.ID,
			DocumentID: job.DocumentID,
			Status:     string(job.Status),
			Error:      job.Error,
			ChunkCount: job.ChunkCount,
			CreatedAt:  job.CreatedAt.Format(time.RFC3339),
			UpdatedAt:  job.UpdatedAt.Format(time.RFC3339),
		})
	}
	c.JSON(http.StatusOK, gin.H{"jobs": views})
}

type statsView struct {
	TotalUsers     int            `json:"total_users"`
	TotalDocuments int            `json:"total_documents"`
	TotalJobs      int            `json:"total_jobs"`
	JobsByStatus   map[string]int `json:"jobs_by_status"`
}

// Stats handles GET /api/v1/admin/stats — the aggregate counts an admin
// dashboard's landing page wants, derived from the same ListAll calls
// the other admin endpoints use rather than a separately maintained
// counter that could drift from reality.
func (h *AdminHandlers) Stats(c *gin.Context) {
	ctx := c.Request.Context()

	users, err := h.authSvc.ListUsers(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to compute stats"})
		return
	}
	docs, err := h.ingestSvc.ListDocuments(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to compute stats"})
		return
	}
	jobs, err := h.ingestSvc.ListJobs(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to compute stats"})
		return
	}

	byStatus := make(map[string]int)
	for _, job := range jobs {
		byStatus[string(job.Status)]++
	}

	c.JSON(http.StatusOK, statsView{
		TotalUsers:     len(users),
		TotalDocuments: len(docs),
		TotalJobs:      len(jobs),
		JobsByStatus:   byStatus,
	})
}
