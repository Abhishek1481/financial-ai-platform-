package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/config"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/health"
)

// startTestServer binds both listeners on ephemeral (":0") ports, starts
// serving in the background, and registers cleanup to shut the server down
// — mirrors the build/start split ml-service's Python test suite uses for
// the same reason: get the real bound port before issuing requests.
func startTestServer(t *testing.T) *Server {
	t.Helper()

	cfg := config.Config{
		Environment:     "test",
		HTTPHost:        "127.0.0.1",
		HTTPPort:        0,
		MetricsHost:     "127.0.0.1",
		MetricsPort:     0,
		LogLevel:        "error",
		ShutdownTimeout: 5 * time.Second,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	readiness := health.NewReadiness()

	server := New(cfg, logger, readiness)
	if err := server.Listen(); err != nil {
		t.Fatalf("Listen() failed: %v", err)
	}

	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- server.Serve() }()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown() failed: %v", err)
		}
		if err := <-serveErrCh; err != nil {
			t.Errorf("Serve() returned error: %v", err)
		}
	})

	return server
}

func TestServer_Healthz(t *testing.T) {
	server := startTestServer(t)

	resp, err := http.Get("http://" + server.HTTPAddr() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf(`body["status"] = %q, want "ok"`, body["status"])
	}
}

func TestServer_Readyz(t *testing.T) {
	server := startTestServer(t)

	resp, err := http.Get("http://" + server.HTTPAddr() + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestServer_MetricsServedOnlyOnMetricsPort(t *testing.T) {
	server := startTestServer(t)

	metricsResp, err := http.Get("http://" + server.MetricsAddr() + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics on metrics port failed: %v", err)
	}
	defer metricsResp.Body.Close()
	if metricsResp.StatusCode != http.StatusOK {
		t.Fatalf("metrics port /metrics status = %d, want %d", metricsResp.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(metricsResp.Body)
	if err != nil {
		t.Fatalf("reading metrics body: %v", err)
	}
	if !strings.Contains(string(body), "gateway_http_requests_total") {
		t.Error("metrics output missing gateway_http_requests_total series")
	}

	publicResp, err := http.Get("http://" + server.HTTPAddr() + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics on public port failed: %v", err)
	}
	defer publicResp.Body.Close()
	if publicResp.StatusCode != http.StatusNotFound {
		t.Errorf("public port /metrics status = %d, want %d (metrics must not be reachable publicly)",
			publicResp.StatusCode, http.StatusNotFound)
	}
}

func TestServer_RequestMetricsAreRecorded(t *testing.T) {
	server := startTestServer(t)

	resp, err := http.Get("http://" + server.HTTPAddr() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz failed: %v", err)
	}
	resp.Body.Close()

	metricsResp, err := http.Get("http://" + server.MetricsAddr() + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics failed: %v", err)
	}
	defer metricsResp.Body.Close()
	body, err := io.ReadAll(metricsResp.Body)
	if err != nil {
		t.Fatalf("reading metrics body: %v", err)
	}

	want := `gateway_http_requests_total{method="GET",route="/healthz",status="200"}`
	if !strings.Contains(string(body), want) {
		t.Errorf("metrics output missing counter for /healthz request; want substring %q, got:\n%s", want, body)
	}
}
