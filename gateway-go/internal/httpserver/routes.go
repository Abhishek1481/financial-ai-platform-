package httpserver

import (
	"github.com/gin-gonic/gin"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/auth"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/handlers"
)

// registerRoutes wires every route this phase knows about. Kept out of
// server.go (which only owns listener lifecycle) so adding a route in a
// later phase never touches bind/serve/shutdown logic.
func registerRoutes(engine *gin.Engine, deps Dependencies, maxUploadBytes int64) {
	engine.GET("/healthz", handlers.Healthz)
	engine.GET("/readyz", deps.Readiness.Handler())

	authMW := auth.NewMiddleware(deps.Tokens)
	authHandlers := handlers.NewAuthHandlers(deps.AuthService)
	documentHandlers := handlers.NewDocumentHandlers(deps.Ingestion, maxUploadBytes)
	searchHandlers := handlers.NewSearchHandlers(deps.Searcher)
	ragHandlers := handlers.NewRAGHandlers(deps.Answerer)

	v1 := engine.Group("/api/v1")

	authGroup := v1.Group("/auth")
	authGroup.POST("/register", authHandlers.Register)
	authGroup.POST("/login", authHandlers.Login)

	v1.GET("/me", authMW.Authenticate(), authHandlers.Me)

	documents := v1.Group("/documents")
	documents.Use(authMW.Authenticate())
	documents.POST("", documentHandlers.Upload)
	documents.GET("/:id", documentHandlers.GetDocument)
	documents.GET("/:id/summary", ragHandlers.Summarize)

	v1.GET("/search", authMW.Authenticate(), searchHandlers.Search)
	v1.POST("/rag/query", authMW.Authenticate(), ragHandlers.Query)

	// Placeholder route proving RBAC actually gates access end-to-end;
	// real admin endpoints (documents/users/jobs/metrics) arrive in
	// Phase 15.
	admin := v1.Group("/admin")
	admin.Use(authMW.Authenticate(), authMW.RequireRole(auth.RoleAdmin))
	admin.GET("/ping", handlers.AdminPing)
}
