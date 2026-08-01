// Package handlers holds HTTP handlers that don't belong to a specific
// domain service (auth, ingestion, search, ...) — currently just liveness.
// Readiness lives in internal/health since it's stateful (a registry of
// checks), not a plain handler function.
package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Healthz is the liveness probe: it only proves the process is up and the
// Gin engine is serving requests. It never checks a downstream dependency —
// that's what /readyz (internal/health.Readiness) is for.
func Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
