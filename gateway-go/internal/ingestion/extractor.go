package ingestion

import (
	"context"

	commonv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/common/v1"
	ingestionv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/ingestion/v1"
)

// Extractor is the ml-service capability Service depends on — an
// interface, not the concrete *mlclient.Client, for the same reason
// DocumentRepository/JobRepository/ObjectStore are interfaces: it lets the
// worker-pool wiring be unit-tested (see service_test.go) without a live
// ml-service process. *mlclient.Client satisfies this structurally; no
// explicit wiring is needed on its side.
type Extractor interface {
	ExtractDocument(
		ctx context.Context,
		documentID, uri string,
		docType commonv1.DocumentType,
	) (*ingestionv1.ExtractDocumentResponse, error)
}
