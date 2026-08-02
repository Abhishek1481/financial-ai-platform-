package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	commonv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/common/v1"
	searchv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/search/v1"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/cache"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/search"
)

type SearchHandlers struct {
	searcher search.Searcher
	cache    cache.Cache
	cacheTTL time.Duration
}

// NewSearchHandlers's cacheTTL of 0 disables caching (every lookup is a
// miss) — a valid configuration (config.Config.SearchCacheTTL defaults to
// nonzero, but a test or a deployment that wants always-fresh results can
// set it to 0 without a separate on/off flag).
func NewSearchHandlers(searcher search.Searcher, c cache.Cache, cacheTTL time.Duration) *SearchHandlers {
	return &SearchHandlers{searcher: searcher, cache: c, cacheTTL: cacheTTL}
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

	tickersCSV := c.Query("tickers")
	filingTypesCSV := c.Query("filing_types")
	fiscalPeriod := c.Query("fiscal_period")
	filter := buildMetadataFilter(tickersCSV, filingTypesCSV, fiscalPeriod)

	cacheKey := fmt.Sprintf("search:%s|%s|%d|%s|%s|%s", query, mode, topK, tickersCSV, filingTypesCSV, fiscalPeriod)
	if cached, ok := h.cache.Get(cacheKey); ok {
		c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(cached))
		return
	}

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

	view := searchResponseView{
		Results:         results,
		SearchLatencyMs: float64(resp.GetSearchLatencyMs()),
	}
	// Caching is best-effort: a marshal failure here (which shouldn't
	// happen for this struct) just means the next identical query is a
	// cache miss too, not a request failure — the response the client
	// already has is unaffected either way.
	if raw, err := json.Marshal(view); err == nil {
		h.cache.Set(cacheKey, string(raw), h.cacheTTL)
	}

	c.JSON(http.StatusOK, view)
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
