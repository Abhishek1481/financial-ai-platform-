// Package mlclient is gateway-go's gRPC client to ml-service — the only
// way any Go code in this repo talks to it (see /docs/ARCHITECTURE.md for
// why that boundary is gRPC, not HTTP). Only the ingestion RPC is wrapped
// so far; Phase 7-12 add the embedding/search/RAG/evaluation clients here
// as those services grow real implementations.
package mlclient

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"

	commonv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/common/v1"
	ingestionv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/ingestion/v1"
)

// Client owns the underlying connection to ml-service and exposes one
// method per RPC gateway-go actually calls — callers never touch the
// generated stub types directly, which keeps a future proto rename from
// rippling past this one file.
type Client struct {
	conn      *grpc.ClientConn
	ingestion ingestionv1.IngestionServiceClient
	health    grpc_health_v1.HealthClient
}

// NewClient dials addr. Dialing is lazy (grpc.NewClient performs no I/O;
// the connection is established on first RPC), so a misconfigured or
// down ml-service doesn't block gateway-go's own startup — it surfaces
// through the readiness check (see HealthCheck) and through individual RPC
// failures instead.
//
// insecure.NewCredentials() means no TLS — acceptable for now because both
// services currently run on the same trusted network (a developer's
// machine, or later, sidecar-adjacent containers in the same pod/task);
// this is the seam where mTLS gets added once that stops being true.
func NewClient(addr string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("mlclient: dial %s: %w", addr, err)
	}
	return &Client{
		conn:      conn,
		ingestion: ingestionv1.NewIngestionServiceClient(conn),
		health:    grpc_health_v1.NewHealthClient(conn),
	}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

// HealthCheck is used as a readiness-probe dependency check (see
// internal/health.Readiness) — it asks ml-service's own standard gRPC
// health service, the same one every ml-service RPC is registered against
// (see ml-service/app/server.py), whether it considers itself serving.
func (c *Client) HealthCheck(ctx context.Context) error {
	resp, err := c.health.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		return fmt.Errorf("mlclient: health check: %w", err)
	}
	if resp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		return fmt.Errorf("mlclient: ml-service reports status %s", resp.GetStatus())
	}
	return nil
}

// ExtractDocument asks ml-service to read the document at uri and extract
// its text, tables, and best-effort metadata.
func (c *Client) ExtractDocument(
	ctx context.Context,
	documentID, uri string,
	docType commonv1.DocumentType,
) (*ingestionv1.ExtractDocumentResponse, error) {
	resp, err := c.ingestion.ExtractDocument(ctx, &ingestionv1.ExtractDocumentRequest{
		DocumentId: documentID,
		S3Uri:      uri,
		DocType:    docType,
	})
	if err != nil {
		return nil, fmt.Errorf("mlclient: ExtractDocument: %w", err)
	}
	return resp, nil
}
