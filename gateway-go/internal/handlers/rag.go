package handlers

import (
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	commonv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/common/v1"
	ragv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/rag/v1"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/rag"
)

type RAGHandlers struct {
	answerer rag.Answerer
}

func NewRAGHandlers(answerer rag.Answerer) *RAGHandlers {
	return &RAGHandlers{answerer: answerer}
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
// "token" (a generated token), "final" (citations/usage/latency, always
// last), or "error" (terminal).
func (h *RAGHandlers) Query(c *gin.Context) {
	var body queryRequestBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	history := make([]*commonv1.ConversationTurn, 0, len(body.History))
	for _, turn := range body.History {
		role, err := parseConversationRole(turn.Role)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		history = append(history, &commonv1.ConversationTurn{Role: role, Content: turn.Content})
	}

	events, err := h.answerer.Query(c.Request.Context(), &ragv1.QueryRequest{
		SessionId: body.SessionID,
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
				Citations: citations,
				Usage: tokenUsageView{
					PromptTokens:     usage.GetPromptTokens(),
					CompletionTokens: usage.GetCompletionTokens(),
					TotalTokens:      usage.GetTotalTokens(),
				},
				LatencyMs: float64(event.Final.GetLatencyMs()),
			})
			return false
		default:
			c.SSEvent("token", gin.H{"token": event.Token})
			return true
		}
	})
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
