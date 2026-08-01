package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

// Logging replaces gin.Logger()'s plain-text output with structured JSON
// via slog, so request logs land in the log aggregator the same shape as
// every other log line this process emits.
func Logging(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		route := c.FullPath()
		if route == "" {
			route = path
		}

		logger.Info("http_request",
			"method", c.Request.Method,
			"path", path,
			"route", route,
			"query", query,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
			"errors", c.Errors.String(),
		)
	}
}
