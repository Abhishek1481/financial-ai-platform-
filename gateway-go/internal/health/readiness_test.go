package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	return c, rec
}

func TestReadiness_NoChecksIsReady(t *testing.T) {
	r := NewReadiness()
	c, rec := newTestContext(t)

	r.Handler()(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if body["status"] != "ready" {
		t.Errorf(`body["status"] = %v, want "ready"`, body["status"])
	}
}

func TestReadiness_PassingCheckIsReady(t *testing.T) {
	r := NewReadiness()
	r.Register("db", func(ctx context.Context) error { return nil })
	c, rec := newTestContext(t)

	r.Handler()(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestReadiness_FailingCheckIsNotReady(t *testing.T) {
	r := NewReadiness()
	r.Register("db", func(ctx context.Context) error { return errors.New("connection refused") })
	c, rec := newTestContext(t)

	r.Handler()(c)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if body["status"] != "not_ready" {
		t.Errorf(`body["status"] = %v, want "not_ready"`, body["status"])
	}
	checks, ok := body["checks"].(map[string]any)
	if !ok {
		t.Fatalf("checks field missing or wrong type: %v", body["checks"])
	}
	if checks["db"] != "connection refused" {
		t.Errorf(`checks["db"] = %v, want "connection refused"`, checks["db"])
	}
}

func TestReadiness_OnePassingOneFailingIsNotReady(t *testing.T) {
	r := NewReadiness()
	r.Register("db", func(ctx context.Context) error { return nil })
	r.Register("cache", func(ctx context.Context) error { return errors.New("timeout") })
	c, rec := newTestContext(t)

	r.Handler()(c)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
