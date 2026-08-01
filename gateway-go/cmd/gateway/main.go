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
	"time"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/auth"
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

	if cfg.JWTSecret == config.InsecureDefaultJWTSecret {
		logger.Warn("using the default JWT signing secret — fine for local development, never for a deployed environment; set GATEWAY_JWT_SECRET")
	}

	// No checks registered yet: auth's user store is in-memory (no
	// dependency to check), Phase 6 adds one for ml-service gRPC
	// connectivity, Phase 13 for Redis, and Postgres once it replaces the
	// in-memory user store. An instance with no known dependencies is
	// trivially ready.
	readiness := health.NewReadiness()

	userRepo := auth.NewMemoryUserRepository()
	tokens := auth.NewTokenService(cfg.JWTSecret, cfg.JWTTTL)
	authService := auth.NewService(userRepo, tokens)

	seedCtx, cancelSeed := context.WithTimeout(context.Background(), 5*time.Second)
	err = authService.SeedAdmin(seedCtx, cfg.AdminEmail, cfg.AdminPassword)
	cancelSeed()
	if err != nil {
		logger.Error("failed to seed admin account", "error", err)
		os.Exit(1)
	}

	server := httpserver.New(cfg, logger, readiness, authService, tokens)
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
