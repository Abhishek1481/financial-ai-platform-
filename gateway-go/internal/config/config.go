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
}

func Load() (Config, error) {
	cfg := Config{
		Environment:     getEnv("GATEWAY_ENVIRONMENT", "development"),
		HTTPHost:        getEnv("GATEWAY_HTTP_HOST", "0.0.0.0"),
		HTTPPort:        0,
		MetricsHost:     getEnv("GATEWAY_METRICS_HOST", "0.0.0.0"),
		MetricsPort:     0,
		LogLevel:        getEnv("GATEWAY_LOG_LEVEL", "info"),
		ShutdownTimeout: 0,
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
