package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// AdminPing exists to prove the RBAC wiring (Authenticate + RequireRole)
// actually gates a route end-to-end. It's a placeholder for real admin
// endpoints — documents/users/jobs/metrics management — that arrive in
// Phase 15 (admin dashboard).
func AdminPing(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": "pong"})
}
