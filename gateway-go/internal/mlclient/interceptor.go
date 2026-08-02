package mlclient

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/reqid"
)

// requestIDMetadataKey must match ml-service's app/observability.py
// REQUEST_ID_METADATA_KEY exactly — it's how a request ID minted by
// internal/middleware.RequestID reaches ml-service's structured logs.
const requestIDMetadataKey = "x-request-id"

var (
	requestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_mlclient_requests_total",
		Help: "Total gRPC requests gateway-go made to ml-service, labeled by method and status code.",
	}, []string{"method", "status"})

	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "gateway_mlclient_request_duration_seconds",
		Help:    "ml-service gRPC call latency in seconds, labeled by method.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method"})
)

// attachRequestID forwards the current request's correlation ID (set by
// internal/middleware.RequestID) as outgoing gRPC metadata, if present —
// absent when a caller invokes mlclient outside an HTTP request's context
// (none do today, but it's not an error case, just nothing to forward).
func attachRequestID(ctx context.Context) context.Context {
	if id, ok := reqid.FromContext(ctx); ok {
		return metadata.AppendToOutgoingContext(ctx, requestIDMetadataKey, id)
	}
	return ctx
}

func recordCall(method string, start time.Time, err error) {
	requestDuration.WithLabelValues(method).Observe(time.Since(start).Seconds())
	requestsTotal.WithLabelValues(method, status.Code(err).String()).Inc()
}

// unaryClientInterceptor and streamClientInterceptor are gateway-go's
// client-side counterpart to ml-service's ObservabilityInterceptor
// (app/observability.py): every RPC gateway-go makes to ml-service is
// instrumented once here, rather than by hand in each mlclient method —
// unary for ExtractDocument/Search/Summarize/EvaluateAnswer, streaming for
// ChunkAndEmbed/Query (both server-streaming, so still a client *stream*
// even though there's one logical request).
func unaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context, method string, req, reply any,
		cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
	) error {
		start := time.Now()
		err := invoker(attachRequestID(ctx), method, req, reply, cc, opts...)
		recordCall(method, start, err)
		return err
	}
}

func streamClientInterceptor() grpc.StreamClientInterceptor {
	return func(
		ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn,
		method string, streamer grpc.Streamer, opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		start := time.Now()
		clientStream, err := streamer(attachRequestID(ctx), desc, cc, method, opts...)
		if err != nil {
			recordCall(method, start, err)
			return nil, err
		}
		// The RPC itself succeeds or fails as the stream is consumed
		// (Recv() returning a non-EOF error), not at stream-open time —
		// wrapping ClientStream lets the metric fire once EOF/error
		// actually arrives, matching what "this call succeeded" means for
		// a streaming RPC.
		return &observedClientStream{ClientStream: clientStream, method: method, start: start}, nil
	}
}

type observedClientStream struct {
	grpc.ClientStream
	method string
	start  time.Time
	done   bool
}

func (s *observedClientStream) RecvMsg(m any) error {
	err := s.ClientStream.RecvMsg(m)
	if err != nil && !s.done { // EOF (clean end) or a real error — either way, the call is over
		s.done = true
		recordCall(s.method, s.start, ioEOFAsNil(err))
	}
	return err
}

// ioEOFAsNil maps io.EOF (a clean stream end, not a status error) to nil
// so recordCall's status.Code(nil) records codes.OK — passing io.EOF
// itself through would record codes.Unknown, since it isn't a grpc status
// error at all.
func ioEOFAsNil(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
