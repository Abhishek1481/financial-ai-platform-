// Package search is deliberately thin: unlike ingestion, there's no
// dedup/job-queue/persistence concern here, just translating an HTTP
// query into a gRPC call and back — so there's no Service layer, only the
// interface gateway-go's handler depends on (same reason
// ingestion.Extractor/Embedder are interfaces: *mlclient.Client satisfies
// this structurally, and tests substitute a fake).
package search

import (
	"context"

	commonv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/common/v1"
	searchv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/search/v1"
)

type Searcher interface {
	Search(
		ctx context.Context,
		query string,
		topK int32,
		mode searchv1.SearchMode,
		filter *commonv1.MetadataFilter,
	) (*searchv1.SearchResponse, error)
}
