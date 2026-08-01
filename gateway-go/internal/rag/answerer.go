// Package rag is deliberately thin, same reasoning as internal/search:
// there's no dedup/job-queue/persistence concern here, just translating an
// HTTP request into a streaming gRPC call and back — so there's no Service
// layer, only the interface gateway-go's handler depends on. *mlclient.Client
// satisfies this structurally; tests substitute a fake.
package rag

import (
	"context"

	ragv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/rag/v1"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/mlclient"
)

type Answerer interface {
	Query(ctx context.Context, req *ragv1.QueryRequest) (<-chan mlclient.QueryEvent, error)
}
