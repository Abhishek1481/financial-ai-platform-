package config

import (
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.Environment != "development" {
		t.Errorf("Environment = %q, want %q", cfg.Environment, "development")
	}
	if cfg.HTTPPort != 8080 {
		t.Errorf("HTTPPort = %d, want %d", cfg.HTTPPort, 8080)
	}
	if cfg.MetricsPort != 9090 {
		t.Errorf("MetricsPort = %d, want %d", cfg.MetricsPort, 9090)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, 10*time.Second)
	}
}

func TestLoad_EnvOverrides(t *testing.T) {
	t.Setenv("GATEWAY_ENVIRONMENT", "production")
	t.Setenv("GATEWAY_HTTP_PORT", "9999")
	t.Setenv("GATEWAY_SHUTDOWN_TIMEOUT", "30s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.Environment != "production" {
		t.Errorf("Environment = %q, want %q", cfg.Environment, "production")
	}
	if cfg.HTTPPort != 9999 {
		t.Errorf("HTTPPort = %d, want %d", cfg.HTTPPort, 9999)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, 30*time.Second)
	}
}

func TestLoad_InvalidIntReturnsError(t *testing.T) {
	t.Setenv("GATEWAY_HTTP_PORT", "not-a-number")

	if _, err := Load(); err == nil {
		t.Fatal("Load() with invalid GATEWAY_HTTP_PORT: expected error, got nil")
	}
}

func TestLoad_InvalidDurationReturnsError(t *testing.T) {
	t.Setenv("GATEWAY_SHUTDOWN_TIMEOUT", "not-a-duration")

	if _, err := Load(); err == nil {
		t.Fatal("Load() with invalid GATEWAY_SHUTDOWN_TIMEOUT: expected error, got nil")
	}
}
