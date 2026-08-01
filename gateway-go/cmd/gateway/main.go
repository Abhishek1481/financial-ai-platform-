// Command gateway is the entrypoint for gateway-go: the platform's only
// public-facing service. It owns auth, ingestion, rate limiting, routing,
// streaming, and caching in later phases — this is the composition root
// where every domain service gets constructed and wired together.
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
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/ingestion"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/logging"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/mlclient"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/storage"
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

	objectStore, err := storage.NewLocalObjectStore(cfg.StorageDir)
	if err != nil {
		logger.Error("failed to initialize object storage", "error", err)
		os.Exit(1)
	}

	mlClient, err := mlclient.NewClient(cfg.MLServiceAddr)
	if err != nil {
		logger.Error("failed to construct ml-service client", "error", err)
		os.Exit(1)
	}
	defer mlClient.Close()
	// ml-service connectivity has no bearing on whether THIS process is
	// alive (that's /healthz) but every kind of document upload needs it
	// to actually finish, so it belongs on /readyz. Dialing above is lazy
	// (no I/O until first use), so a down ml-service at gateway-go startup
	// shows up here, not as a boot failure.
	readiness.Register("ml-service", mlClient.HealthCheck)

	ingestionService := ingestion.NewService(
		logger,
		ingestion.NewMemoryDocumentRepository(),
		ingestion.NewMemoryJobRepository(),
		objectStore,
		mlClient,
		mlClient,
		cfg.IngestionWorkers,
		cfg.IngestionQueueSize,
	)
	// Runs against a long-lived context independent of any single HTTP
	// request: extraction happens after the upload request that queued it
	// has already returned 202.
	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	defer cancelWorkers()
	ingestionService.Start(workerCtx)

	server := httpserver.New(cfg, logger, httpserver.Dependencies{
		Readiness:   readiness,
		AuthService: authService,
		Tokens:      tokens,
		Ingestion:   ingestionService,
		Searcher:    mlClient,
	})
	if err := server.Listen(); err != nil {
		logger.Error("failed to bind listeners", "error", err)
		os.Exit(1)
	}
	logger.Info("gateway-go listening",
		"http_addr", server.HTTPAddr(),
		"metrics_addr", server.MetricsAddr(),
		"environment", cfg.Environment,
		"ml_service_addr", cfg.MLServiceAddr,
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

	// Only after the HTTP server has stopped accepting new uploads is it
	// safe to drain the ingestion queue — draining first could still see
	// new work arrive from in-flight upload requests.
	if err := ingestionService.Stop(shutdownCtx); err != nil {
		logger.Error("ingestion worker pool did not drain before shutdown deadline", "error", err)
	}

	logger.Info("gateway-go stopped")
}
