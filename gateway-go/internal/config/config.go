// Package config loads gateway-go's runtime configuration from environment
// variables. Every setting has a sane default so the service runs out of
// the box in local development; production overrides them via the
// container's env (see docker/gateway-go.Dockerfile, Phase 16).
//
// Hand-rolled rather than pulled in via viper/envconfig: six scalar fields
// don't justify a dependency, and this mirrors the same "typed config,
// validated once at startup, not on first use" shape as ml-service's
// pydantic-settings model (see ml-service/app/config.py) without needing a
// third-party parser to get there in Go.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Environment string

	HTTPHost string
	HTTPPort int

	// Metrics are served on a separate port from public API traffic so a
	// Kubernetes Service/Ingress can expose HTTPPort without also exposing
	// internal request-rate and latency data — only an in-cluster
	// Prometheus needs MetricsPort.
	MetricsHost string
	MetricsPort int

	LogLevel string

	// Upper bound on how long graceful shutdown waits for in-flight
	// requests to finish before the process exits anyway.
	ShutdownTimeout time.Duration

	// JWTSecret signs and verifies every access token (see
	// internal/auth.TokenService). InsecureDefaultJWTSecret is only ever
	// acceptable in development — Load rejects it in production.
	JWTSecret string
	JWTTTL    time.Duration

	// AdminEmail/AdminPassword seed the one admin account this phase
	// supports (see auth.Service.SeedAdmin) — there is no self-service
	// path to becoming an admin. InsecureDefaultAdminPassword is rejected
	// in production for the same reason as the JWT secret default.
	AdminEmail    string
	AdminPassword string

	// MLServiceAddr is where the ml-service gRPC server (see
	// ml-service/app/server.py) is reachable.
	MLServiceAddr string

	// StorageDir is where LocalObjectStore writes uploaded files — the
	// dev/test stand-in for real object storage (Phase 16). Relative
	// paths resolve against the process's working directory.
	StorageDir string

	MaxUploadSizeBytes int64

	// IngestionWorkers/IngestionQueueSize size the bounded worker pool
	// that calls ml-service to extract each uploaded document (see
	// internal/worker.Pool) — the actual backpressure mechanism behind
	// "concurrent document ingestion."
	IngestionWorkers   int
	IngestionQueueSize int
}

const (
	InsecureDefaultJWTSecret     = "dev-insecure-secret-change-me"
	InsecureDefaultAdminPassword = "dev-insecure-admin-change-me"
	defaultAdminEmail            = "admin@example.com"
)

func Load() (Config, error) {
	cfg := Config{
		Environment:     getEnv("GATEWAY_ENVIRONMENT", "development"),
		HTTPHost:        getEnv("GATEWAY_HTTP_HOST", "0.0.0.0"),
		HTTPPort:        0,
		MetricsHost:     getEnv("GATEWAY_METRICS_HOST", "0.0.0.0"),
		MetricsPort:     0,
		LogLevel:        getEnv("GATEWAY_LOG_LEVEL", "info"),
		ShutdownTimeout: 0,
		JWTSecret:       getEnv("GATEWAY_JWT_SECRET", InsecureDefaultJWTSecret),
		JWTTTL:          0,
		AdminEmail:      getEnv("GATEWAY_ADMIN_EMAIL", defaultAdminEmail),
		AdminPassword:   getEnv("GATEWAY_ADMIN_PASSWORD", InsecureDefaultAdminPassword),
		MLServiceAddr:   getEnv("GATEWAY_ML_SERVICE_ADDR", "localhost:50051"),
		StorageDir:      getEnv("GATEWAY_STORAGE_DIR", "./data/documents"),
	}

	var err error
	if cfg.HTTPPort, err = getEnvInt("GATEWAY_HTTP_PORT", 8080); err != nil {
		return Config{}, err
	}
	if cfg.MetricsPort, err = getEnvInt("GATEWAY_METRICS_PORT", 9090); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = getEnvDuration("GATEWAY_SHUTDOWN_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.JWTTTL, err = getEnvDuration("GATEWAY_JWT_TTL", time.Hour); err != nil {
		return Config{}, err
	}

	maxUploadMB, err := getEnvInt("GATEWAY_MAX_UPLOAD_SIZE_MB", 25)
	if err != nil {
		return Config{}, err
	}
	cfg.MaxUploadSizeBytes = int64(maxUploadMB) << 20

	if cfg.IngestionWorkers, err = getEnvInt("GATEWAY_INGESTION_WORKERS", 4); err != nil {
		return Config{}, err
	}
	if cfg.IngestionQueueSize, err = getEnvInt("GATEWAY_INGESTION_QUEUE_SIZE", 100); err != nil {
		return Config{}, err
	}

	if cfg.Environment == "production" {
		if cfg.JWTSecret == InsecureDefaultJWTSecret {
			return Config{}, fmt.Errorf("config: GATEWAY_JWT_SECRET must be set in production")
		}
		if cfg.AdminPassword == InsecureDefaultAdminPassword {
			return Config{}, fmt.Errorf("config: GATEWAY_ADMIN_PASSWORD must be set in production")
		}
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("config: %s=%q is not a valid integer: %w", key, v, err)
	}
	return n, nil
}

func getEnvDuration(key string, fallback time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("config: %s=%q is not a valid duration: %w", key, v, err)
	}
	return d, nil
}
