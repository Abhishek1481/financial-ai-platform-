// Command gateway is the entrypoint for gateway-go: the platform's only
// public-facing service. It owns auth, rate limiting, routing, streaming,
// and caching in later phases — this phase wires up config, structured
// logging, health/readiness/metrics, and graceful shutdown, the load-
// bearing plumbing every later phase builds on.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/config"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/health"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/httpserver"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/logging"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	logger := logging.New(cfg.LogLevel)
	slog.SetDefault(logger)

	// No checks registered yet — Phase 5 adds one for the JWT signing
	// key/DB, Phase 6 for ml-service gRPC connectivity, Phase 13 for
	// Redis. An instance with no known dependencies is trivially ready.
	readiness := health.NewReadiness()

	server := httpserver.New(cfg, logger, readiness)
	if err := server.Listen(); err != nil {
		logger.Error("failed to bind listeners", "error", err)
		os.Exit(1)
	}
	logger.Info("gateway-go listening",
		"http_addr", server.HTTPAddr(),
		"metrics_addr", server.MetricsAddr(),
		"environment", cfg.Environment,
	)

	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- server.Serve() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serveErrCh:
		if err != nil {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining in-flight requests")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("gateway-go stopped")
}
