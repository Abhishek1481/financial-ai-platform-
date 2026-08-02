package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/reqid"
)

// Logging replaces gin.Logger()'s plain-text output with structured JSON
// via slog, so request logs land in the log aggregator the same shape as
// every other log line this process emits. Includes request_id (see
// RequestID, which must run first) so this line can be grepped alongside
// the ml-service log lines the same request produced — see
// ml-service/app/tracing.py for the other half of that correlation.
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

		requestID, _ := reqid.FromContext(c.Request.Context())

		logger.Info("http_request",
			"method", c.Request.Method,
			"path", path,
			"route", route,
			"query", query,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
			"errors", c.Errors.String(),
			"request_id", requestID,
		)
	}
}
