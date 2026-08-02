package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/evaluation"
)

// AdminPing exists to prove the RBAC wiring (Authenticate + RequireRole)
// actually gates a route end-to-end. It's a placeholder for real admin
// endpoints — documents/users/jobs/metrics management — that arrive in
// Phase 15 (admin dashboard).
func AdminPing(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": "pong"})
}

type AdminHandlers struct {
	evaluator evaluation.Evaluator
}

func NewAdminHandlers(evaluator evaluation.Evaluator) *AdminHandlers {
	return &AdminHandlers{evaluator: evaluator}
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
