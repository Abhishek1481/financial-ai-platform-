package handlers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/common/v1"
	ragv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/rag/v1"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/conversation"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/rag"
)

type RAGHandlers struct {
	answerer      rag.Answerer
	conversations conversation.Store
}

func NewRAGHandlers(answerer rag.Answerer, conversations conversation.Store) *RAGHandlers {
	return &RAGHandlers{answerer: answerer, conversations: conversations}
}

type conversationTurnBody struct {
	Role    string `json:"role" binding:"required,oneof=user assistant"`
	Content string `json:"content" binding:"required"`
}

type queryRequestBody struct {
	SessionID    string                 `json:"session_id"`
	Question     string                 `json:"question" binding:"required"`
	History      []conversationTurnBody `json:"history"`
	TopK         int32                  `json:"top_k"`
	Tickers      []string               `json:"tickers"`
	FilingTypes  []string               `json:"filing_types"`
	FiscalPeriod string                 `json:"fiscal_period"`
}

type citationView struct {
	ChunkID    string `json:"chunk_id"`
	DocumentID string `json:"document_id"`
	Quote      string `json:"quote"`
	PageNumber int32  `json:"page_number"`
	SourceURL  string `json:"source_url"`
}

type tokenUsageView struct {
	PromptTokens     int32 `json:"prompt_tokens"`
	CompletionTokens int32 `json:"completion_tokens"`
	TotalTokens      int32 `json:"total_tokens"`
}

type queryFinalView struct {
	SessionID string         `json:"session_id"`
	Citations []citationView `json:"citations"`
	Usage     tokenUsageView `json:"usage"`
	LatencyMs float64        `json:"latency_ms"`
}

// Query handles POST /api/v1/rag/query — a Server-Sent Events stream, not a
// single JSON response: ml-service's RAGService.Query is server-streaming
// precisely so a generated answer can reach the caller token-by-token
// (see proto/README.md's "Why these RPC shapes"), and this handler relays
// that live rather than buffering the full answer like documentHandlers
// does for ChunkAndEmbed's progress stream. Each SSE event is one of
// "token" (a generated token), "final" (session_id/citations/usage/latency,
// always last), or "error" (terminal).
//
// Conversation memory is server-side, keyed by session_id: an omitted
// session_id gets one minted here (returned in the "final" event so a
// client can reuse it for follow-ups) and its prior turns are loaded from
// conversation.Store automatically — a caller doesn't need to resend the
// whole transcript on every turn. Explicitly supplying "history" in the
// request body overrides that lookup instead (the stateless mode Phase 9
// shipped, still useful for a caller managing its own transcript). Either
// way, the question and generated answer are appended to the session's
// stored history once the answer finishes, so the next turn sees it.
func (h *RAGHandlers) Query(c *gin.Context) {
	var body queryRequestBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	sessionID := body.SessionID
	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	history, err := h.resolveHistory(c.Request.Context(), sessionID, body.History)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	events, err := h.answerer.Query(c.Request.Context(), &ragv1.QueryRequest{
		SessionId: sessionID,
		Question:  body.Question,
		History:   history,
		Filter:    buildMetadataFilterFromLists(body.Tickers, body.FilingTypes, body.FiscalPeriod),
		TopK:      body.TopK,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "rag query failed"})
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")

	var answer strings.Builder
	c.Stream(func(w io.Writer) bool {
		event, ok := <-events
		if !ok {
			return false
		}
		switch {
		case event.Err != nil:
			c.SSEvent("error", gin.H{"error": event.Err.Error()})
			return false
		case event.Final != nil:
			citations := make([]citationView, 0, len(event.Final.GetCitations()))
			for _, cit := range event.Final.GetCitations() {
				citations = append(citations, citationView{
					ChunkID:    cit.GetChunkId(),
					DocumentID: cit.GetDocumentId(),
					Quote:      cit.GetQuote(),
					PageNumber: cit.GetPageNumber(),
					SourceURL:  cit.GetSourceUrl(),
				})
			}
			usage := event.Final.GetUsage()
			c.SSEvent("final", queryFinalView{
				SessionID: sessionID,
				Citations: citations,
				Usage: tokenUsageView{
					PromptTokens:     usage.GetPromptTokens(),
					CompletionTokens: usage.GetCompletionTokens(),
					TotalTokens:      usage.GetTotalTokens(),
				},
				LatencyMs: float64(event.Final.GetLatencyMs()),
			})
			h.persistTurns(c.Request.Context(), sessionID, body.Question, answer.String())
			return false
		default:
			answer.WriteString(event.Token)
			c.SSEvent("token", gin.H{"token": event.Token})
			return true
		}
	})
}

// resolveHistory implements the precedence documented on Query: an
// explicit request-body history wins; otherwise the session's stored
// history (possibly empty, for a brand new session) is used.
func (h *RAGHandlers) resolveHistory(
	ctx context.Context, sessionID string, bodyHistory []conversationTurnBody,
) ([]*commonv1.ConversationTurn, error) {
	if len(bodyHistory) > 0 {
		history := make([]*commonv1.ConversationTurn, 0, len(bodyHistory))
		for _, turn := range bodyHistory {
			role, err := parseConversationRole(turn.Role)
			if err != nil {
				return nil, err
			}
			history = append(history, &commonv1.ConversationTurn{Role: role, Content: turn.Content})
		}
		return history, nil
	}

	stored, err := h.conversations.History(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load conversation history: %w", err)
	}
	history := make([]*commonv1.ConversationTurn, 0, len(stored))
	for _, turn := range stored {
		history = append(history, &commonv1.ConversationTurn{
			Role:    conversationRoleToProto(turn.Role),
			Content: turn.Content,
		})
	}
	return history, nil
}

// persistTurns is best-effort: conversation memory is a UX convenience
// (skip resending the transcript), not a correctness requirement, so a
// storage failure here logs implicitly via the returned error being
// dropped rather than failing a response the client already received.
func (h *RAGHandlers) persistTurns(ctx context.Context, sessionID, question, answer string) {
	now := time.Now()
	_ = h.conversations.AppendTurns(ctx, sessionID,
		conversation.Turn{Role: conversation.RoleUser, Content: question, CreatedAt: now},
		conversation.Turn{Role: conversation.RoleAssistant, Content: answer, CreatedAt: now},
	)
}

type summarizeResponseView struct {
	Summary   string         `json:"summary"`
	Citations []citationView `json:"citations"`
	Usage     tokenUsageView `json:"usage"`
	LatencyMs float64        `json:"latency_ms"`
}

// Summarize handles GET /api/v1/documents/:id/summary?type=executive|risk|
// revenue|sentiment (default executive) — unary, unlike Query, since a
// summary has no "watch it stream" requirement.
func (h *RAGHandlers) Summarize(c *gin.Context) {
	documentID := c.Param("id")

	summaryType, err := parseSummaryType(c.Query("type"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.answerer.Summarize(c.Request.Context(), documentID, summaryType)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "document has no embedded chunks"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "summarize failed"})
		return
	}

	citations := make([]citationView, 0, len(resp.GetCitations()))
	for _, cit := range resp.GetCitations() {
		citations = append(citations, citationView{
			ChunkID:    cit.GetChunkId(),
			DocumentID: cit.GetDocumentId(),
			Quote:      cit.GetQuote(),
			PageNumber: cit.GetPageNumber(),
			SourceURL:  cit.GetSourceUrl(),
		})
	}
	usage := resp.GetUsage()
	c.JSON(http.StatusOK, summarizeResponseView{
		Summary:   resp.GetSummary(),
		Citations: citations,
		Usage: tokenUsageView{
			PromptTokens:     usage.GetPromptTokens(),
			CompletionTokens: usage.GetCompletionTokens(),
			TotalTokens:      usage.GetTotalTokens(),
		},
		LatencyMs: float64(resp.GetLatencyMs()),
	})
}

func parseSummaryType(raw string) (ragv1.SummaryType, error) {
	switch raw {
	case "", "executive":
		return ragv1.SummaryType_SUMMARY_TYPE_EXECUTIVE, nil
	case "risk":
		return ragv1.SummaryType_SUMMARY_TYPE_RISK, nil
	case "revenue":
		return ragv1.SummaryType_SUMMARY_TYPE_REVENUE, nil
	case "sentiment":
		return ragv1.SummaryType_SUMMARY_TYPE_SENTIMENT, nil
	default:
		return ragv1.SummaryType_SUMMARY_TYPE_UNSPECIFIED,
			fmt.Errorf("invalid type %q: must be executive, risk, revenue, or sentiment", raw)
	}
}

func parseConversationRole(raw string) (commonv1.ConversationRole, error) {
	switch raw {
	case "user":
		return commonv1.ConversationRole_CONVERSATION_ROLE_USER, nil
	case "assistant":
		return commonv1.ConversationRole_CONVERSATION_ROLE_ASSISTANT, nil
	default:
		return commonv1.ConversationRole_CONVERSATION_ROLE_UNSPECIFIED,
			fmt.Errorf("invalid history role %q: must be user or assistant", raw)
	}
}

// conversationRoleToProto converts a stored conversation.Turn's role to the
// proto enum — the counterpart to parseConversationRole, which converts the
// other direction (JSON request body -> proto) for explicitly-supplied
// history.
func conversationRoleToProto(role conversation.Role) commonv1.ConversationRole {
	if role == conversation.RoleAssistant {
		return commonv1.ConversationRole_CONVERSATION_ROLE_ASSISTANT
	}
	return commonv1.ConversationRole_CONVERSATION_ROLE_USER
}

// buildMetadataFilterFromLists mirrors buildMetadataFilter (search.go) but
// takes already-parsed slices — the RAG request body is JSON with real
// arrays, unlike search's CSV query parameters, so there's no comma-split
// step to share.
func buildMetadataFilterFromLists(tickers, filingTypes []string, fiscalPeriod string) *commonv1.MetadataFilter {
	if len(tickers) == 0 && len(filingTypes) == 0 && fiscalPeriod == "" {
		return nil
	}
	return &commonv1.MetadataFilter{
		Tickers:      tickers,
		FilingTypes:  filingTypes,
		FiscalPeriod: fiscalPeriod,
	}
}
