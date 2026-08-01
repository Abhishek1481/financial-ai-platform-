package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	commonv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/common/v1"
	searchv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/search/v1"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/search"
)

type SearchHandlers struct {
	searcher search.Searcher
}

func NewSearchHandlers(searcher search.Searcher) *SearchHandlers {
	return &SearchHandlers{searcher: searcher}
}

const defaultSearchTopK = 10

type searchResultView struct {
	ChunkID    string                `json:"chunk_id"`
	DocumentID string                `json:"document_id"`
	Text       string                `json:"text"`
	Score      float32               `json:"score"`
	Metadata   *inferredMetadataView `json:"metadata,omitempty"`
}

type searchResponseView struct {
	Results         []searchResultView `json:"results"`
	SearchLatencyMs float64            `json:"search_latency_ms"`
}

// Search handles GET /api/v1/search?q=...&mode=semantic|keyword|hybrid
// &top_k=10&tickers=AAPL,TSLA&filing_types=10-K&fiscal_period=FY2025-Q1.
// Query-string parsing lives here; everything past that is a single call
// into search.Searcher — there's no business logic of this handler's own
// to test in isolation from the RPC it wraps.
func (h *SearchHandlers) Search(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing required query parameter 'q'"})
		return
	}

	topK := int32(defaultSearchTopK)
	if raw := c.Query("top_k"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "top_k must be a positive integer"})
			return
		}
		topK = int32(parsed)
	}

	mode, err := parseSearchMode(c.Query("mode"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	filter := buildMetadataFilter(c.Query("tickers"), c.Query("filing_types"), c.Query("fiscal_period"))

	resp, err := h.searcher.Search(c.Request.Context(), query, topK, mode, filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "search failed"})
		return
	}

	results := make([]searchResultView, 0, len(resp.GetResults()))
	for _, r := range resp.GetResults() {
		chunk := r.GetChunk()
		results = append(results, searchResultView{
			ChunkID:    chunk.GetChunkId(),
			DocumentID: chunk.GetDocumentId(),
			Text:       chunk.GetText(),
			Score:      r.GetScore(),
			Metadata:   protoMetadataView(chunk.GetMetadata()),
		})
	}

	c.JSON(http.StatusOK, searchResponseView{
		Results:         results,
		SearchLatencyMs: float64(resp.GetSearchLatencyMs()),
	})
}

func parseSearchMode(raw string) (searchv1.SearchMode, error) {
	switch strings.ToLower(raw) {
	case "", "hybrid":
		return searchv1.SearchMode_SEARCH_MODE_HYBRID, nil
	case "semantic":
		return searchv1.SearchMode_SEARCH_MODE_SEMANTIC, nil
	case "keyword":
		return searchv1.SearchMode_SEARCH_MODE_KEYWORD, nil
	default:
		return searchv1.SearchMode_SEARCH_MODE_UNSPECIFIED,
			fmt.Errorf("invalid mode %q: must be semantic, keyword, or hybrid", raw)
	}
}

// buildMetadataFilter returns nil (no filter, not an empty one) when
// nothing was actually specified — ml-service's build_filter treats a nil
// filter as "match everything" and an empty-but-present message the same
// way, but nil is the honest representation of "the caller didn't ask
// for filtering."
func buildMetadataFilter(tickersCSV, filingTypesCSV, fiscalPeriod string) *commonv1.MetadataFilter {
	tickers := splitCSV(tickersCSV)
	filingTypes := splitCSV(filingTypesCSV)
	if len(tickers) == 0 && len(filingTypes) == 0 && fiscalPeriod == "" {
		return nil
	}
	return &commonv1.MetadataFilter{
		Tickers:      tickers,
		FilingTypes:  filingTypes,
		FiscalPeriod: fiscalPeriod,
	}
}

func splitCSV(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// protoMetadataView mirrors metadataView (documents.go) but converts from
// the proto FinancialMetadata search results carry directly, rather than
// from the ingestion.InferredMetadata domain type — the two call sites
// have different source types for the same destination view.
func protoMetadataView(md *commonv1.FinancialMetadata) *inferredMetadataView {
	if md == nil {
		return nil
	}
	if md.GetTicker() == "" && md.GetCompanyName() == "" && md.GetFilingType() == "" && md.GetFiscalPeriod() == "" {
		return nil
	}
	return &inferredMetadataView{
		Ticker:       md.GetTicker(),
		CompanyName:  md.GetCompanyName(),
		FilingType:   md.GetFilingType(),
		FiscalPeriod: md.GetFiscalPeriod(),
	}
}
