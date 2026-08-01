package ingestion

import (
	"context"

	commonv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/common/v1"
	embeddingsv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/embeddings/v1"
)

// Embedder is the ml-service embedding capability Service depends on — an
// interface, not the concrete *mlclient.Client, for the same reason
// Extractor is (see extractor.go): *mlclient.Client satisfies it
// structurally, and tests substitute a fake that never opens a socket.
type Embedder interface {
	ChunkAndEmbed(
		ctx context.Context,
		documentID, rawText string,
		metadata *commonv1.FinancialMetadata,
	) (*embeddingsv1.ChunkAndEmbedProgress, error)
}
