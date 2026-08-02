// Package mlclient is gateway-go's gRPC client to ml-service — the only
// way any Go code in this repo talks to it (see /docs/ARCHITECTURE.md for
// why that boundary is gRPC, not HTTP). Ingestion, embedding, and search
// are wrapped so far; Phase 9-12 add the RAG/evaluation clients here as
// those services grow real implementations.
package mlclient

import (
	"context"
	"errors"
	"fmt"
	"io"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"

	commonv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/common/v1"
	embeddingsv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/embeddings/v1"
	evaluationv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/evaluation/v1"
	ingestionv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/ingestion/v1"
	ragv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/rag/v1"
	searchv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/search/v1"
)

// Client owns the underlying connection to ml-service and exposes one
// method per RPC gateway-go actually calls — callers never touch the
// generated stub types directly, which keeps a future proto rename from
// rippling past this one file.
type Client struct {
	conn       *grpc.ClientConn
	ingestion  ingestionv1.IngestionServiceClient
	embeddings embeddingsv1.EmbeddingServiceClient
	search     searchv1.SearchServiceClient
	rag        ragv1.RAGServiceClient
	evaluation evaluationv1.EvaluationServiceClient
	health     grpc_health_v1.HealthClient
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
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(unaryClientInterceptor()),
		grpc.WithChainStreamInterceptor(streamClientInterceptor()),
	)
	if err != nil {
		return nil, fmt.Errorf("mlclient: dial %s: %w", addr, err)
	}
	return &Client{
		conn:       conn,
		ingestion:  ingestionv1.NewIngestionServiceClient(conn),
		embeddings: embeddingsv1.NewEmbeddingServiceClient(conn),
		search:     searchv1.NewSearchServiceClient(conn),
		rag:        ragv1.NewRAGServiceClient(conn),
		evaluation: evaluationv1.NewEvaluationServiceClient(conn),
		health:     grpc_health_v1.NewHealthClient(conn),
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

// ChunkAndEmbed asks ml-service to chunk, embed, and upsert rawText into
// its vector store. The RPC is server-streaming (see proto/README.md's
// "Why these RPC shapes" — a worker reports live progress on a job's
// status row rather than blocking on one giant response for a large
// document), but this method drains the stream itself and returns only
// the final message: gateway-go's Job model currently tracks coarse
// stages (extracting/embedding/completed), not Python's fine-grained
// chunking/deduping/embedding/upserting sub-stages, so there is nothing
// meaningful to do with the intermediate messages yet. Streaming them
// through to a client (e.g. over SSE) is a natural extension once the
// admin dashboard (Phase 15) wants live upload progress.
func (c *Client) ChunkAndEmbed(
	ctx context.Context,
	documentID, rawText string,
	metadata *commonv1.FinancialMetadata,
) (*embeddingsv1.ChunkAndEmbedProgress, error) {
	stream, err := c.embeddings.ChunkAndEmbed(ctx, &embeddingsv1.ChunkAndEmbedRequest{
		DocumentId: documentID,
		RawText:    rawText,
		Metadata:   metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("mlclient: ChunkAndEmbed: %w", err)
	}

	var final *embeddingsv1.ChunkAndEmbedProgress
	for {
		progress, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("mlclient: ChunkAndEmbed stream: %w", err)
		}
		final = progress
	}
	if final == nil {
		return nil, fmt.Errorf("mlclient: ChunkAndEmbed: server closed the stream without sending any progress")
	}
	return final, nil
}

// Search asks ml-service to run semantic/keyword/hybrid retrieval over
// embedded chunks. Unary and on the request path — a user is waiting on
// this call, unlike ExtractDocument/ChunkAndEmbed which run on a
// background worker.
func (c *Client) Search(
	ctx context.Context,
	query string,
	topK int32,
	mode searchv1.SearchMode,
	filter *commonv1.MetadataFilter,
) (*searchv1.SearchResponse, error) {
	resp, err := c.search.Search(ctx, &searchv1.SearchRequest{
		Query:  query,
		TopK:   topK,
		Mode:   mode,
		Filter: filter,
	})
	if err != nil {
		return nil, fmt.Errorf("mlclient: Search: %w", err)
	}
	return resp, nil
}

// QueryEvent is one item off the channel Query returns: either a generated
// token, the final message (citations/usage/latency), or a terminal error —
// never more than one of the three on a given event.
type QueryEvent struct {
	Token string
	Final *ragv1.QueryFinal
	Err   error
}

// Query asks ml-service to answer a question over retrieved context,
// streaming generated tokens live rather than draining the whole answer
// like ChunkAndEmbed does — this is the reason the Go<->Python boundary
// supports streaming at all (see proto/README.md). The returned channel is
// closed once the final message has been sent or an error occurs; the
// receiving goroutine (internal/handlers/rag.go, forwarding over SSE) reads
// until it closes rather than this method blocking on the whole stream.
func (c *Client) Query(ctx context.Context, req *ragv1.QueryRequest) (<-chan QueryEvent, error) {
	stream, err := c.rag.Query(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("mlclient: Query: %w", err)
	}

	ch := make(chan QueryEvent)
	go func() {
		defer close(ch)
		for {
			chunk, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				ch <- QueryEvent{Err: fmt.Errorf("mlclient: Query stream: %w", err)}
				return
			}
			switch payload := chunk.GetPayload().(type) {
			case *ragv1.QueryResponseChunk_Token:
				ch <- QueryEvent{Token: payload.Token}
			case *ragv1.QueryResponseChunk_Final:
				ch <- QueryEvent{Final: payload.Final}
			}
		}
	}()
	return ch, nil
}

// Summarize asks ml-service to generate one of the fixed summary types
// (executive/risk/revenue/sentiment) over every embedded chunk of a
// document. Unary — unlike Query, a summary has no "watch it stream" UX
// requirement, so ml-service generates the whole thing before responding.
func (c *Client) Summarize(
	ctx context.Context,
	documentID string,
	summaryType ragv1.SummaryType,
) (*ragv1.SummarizeResponse, error) {
	resp, err := c.rag.Summarize(ctx, &ragv1.SummarizeRequest{
		DocumentId: documentID,
		Type:       summaryType,
	})
	if err != nil {
		return nil, fmt.Errorf("mlclient: Summarize: %w", err)
	}
	return resp, nil
}

// EvaluateAnswer asks ml-service to score a single question/answer/context
// triple (faithfulness, context precision/recall, hallucination, answer
// relevancy — see proto/evaluation/v1/evaluation.proto). Unary, for
// on-demand spot-checks; BatchEvaluate (client-streaming, for offline
// eval-regression gates) has no Go-side caller yet — that lands with the
// CI pipeline in Phase 19, which drives it directly over gRPC rather than
// through gateway-go.
func (c *Client) EvaluateAnswer(
	ctx context.Context,
	question, answer string,
	contextTexts []string,
	groundTruthAnswer string,
) (*evaluationv1.EvaluateAnswerResponse, error) {
	retrievedContext := make([]*commonv1.Chunk, 0, len(contextTexts))
	for _, text := range contextTexts {
		retrievedContext = append(retrievedContext, &commonv1.Chunk{Text: text})
	}

	resp, err := c.evaluation.EvaluateAnswer(ctx, &evaluationv1.EvaluateAnswerRequest{
		Question:          question,
		Answer:            answer,
		RetrievedContext:  retrievedContext,
		GroundTruthAnswer: groundTruthAnswer,
	})
	if err != nil {
		return nil, fmt.Errorf("mlclient: EvaluateAnswer: %w", err)
	}
	return resp, nil
}
