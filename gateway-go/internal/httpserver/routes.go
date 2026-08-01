package httpserver

import (
	"github.com/gin-gonic/gin"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/auth"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/handlers"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/health"
)

// registerRoutes wires every route this phase knows about. Kept out of
// server.go (which only owns listener lifecycle) so adding a route in a
// later phase never touches bind/serve/shutdown logic.
func registerRoutes(engine *gin.Engine, readiness *health.Readiness, authService *auth.Service, tokens *auth.TokenService) {
	engine.GET("/healthz", handlers.Healthz)
	engine.GET("/readyz", readiness.Handler())

	authMW := auth.NewMiddleware(tokens)
	authHandlers := handlers.NewAuthHandlers(authService)

	v1 := engine.Group("/api/v1")

	authGroup := v1.Group("/auth")
	authGroup.POST("/register", authHandlers.Register)
	authGroup.POST("/login", authHandlers.Login)

	v1.GET("/me", authMW.Authenticate(), authHandlers.Me)

	// Placeholder route proving RBAC actually gates access end-to-end;
	// real admin endpoints (documents/users/jobs/metrics) arrive in
	// Phase 15.
	admin := v1.Group("/admin")
	admin.Use(authMW.Authenticate(), authMW.RequireRole(auth.RoleAdmin))
	admin.GET("/ping", handlers.AdminPing)
}
