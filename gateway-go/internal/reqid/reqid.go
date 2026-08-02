// Package reqid carries a per-HTTP-request correlation ID through a
// context.Context so it can be attached as gRPC metadata on every ml-service
// call made while handling that request (see internal/mlclient's client
// interceptor) — the gateway-go side of the request-ID log correlation
// scheme documented in ml-service/app/tracing.py.
package reqid

import "context"

type contextKey struct{}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

func FromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(contextKey{}).(string)
	return id, ok
}
