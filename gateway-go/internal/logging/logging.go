// Package logging configures structured (JSON) logging via the stdlib
// log/slog package — no third-party logging library needed, since slog's
// JSON handler is exactly the "one JSON object per line" shape every other
// service in this platform emits (see ml-service/app/logging.py).
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// New builds a JSON slog.Logger writing to stdout at the given level
// ("debug", "info", "warn", "error"; unrecognized values fall back to info).
func New(level string) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLevel(level),
	})
	return slog.New(handler)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
