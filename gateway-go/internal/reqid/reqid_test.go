package reqid

import (
	"context"
	"testing"
)

func TestFromContext_MissingReturnsFalse(t *testing.T) {
	if _, ok := FromContext(context.Background()); ok {
		t.Error("expected ok=false on a context with no request ID")
	}
}

func TestWithRequestID_RoundTrips(t *testing.T) {
	ctx := WithRequestID(context.Background(), "req-123")

	id, ok := FromContext(ctx)
	if !ok || id != "req-123" {
		t.Errorf("FromContext() = (%q, %v), want (\"req-123\", true)", id, ok)
	}
}
