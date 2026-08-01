// Package httpserver wires the Gin engine, middleware, and routes into two
// http.Server instances (public API, internal metrics) and owns their
// lifecycle: bind, serve, and graceful shutdown.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/config"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/handlers"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/health"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/metrics"
	appmiddleware "github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/middleware"
)

// Server owns the public API listener and the internal metrics listener.
// Listen and Serve are separate steps (rather than one blocking call) so
// callers — main.go and tests alike — can learn the actual bound ports
// (needed when a port of 0 asks the OS to pick one) before the blocking
// accept loop starts.
type Server struct {
	logger *slog.Logger

	httpAddr    string
	metricsAddr string

	httpServer    *http.Server
	metricsServer *http.Server

	httpListener    net.Listener
	metricsListener net.Listener
}

func New(cfg config.Config, logger *slog.Logger, readiness *health.Readiness) *Server {
	gin.SetMode(ginMode(cfg.Environment))

	engine := gin.New()
	engine.Use(
		appmiddleware.Recovery(logger),
		appmiddleware.Logging(logger),
		metrics.Middleware(),
	)
	engine.GET("/healthz", handlers.Healthz)
	engine.GET("/readyz", readiness.Handler())

	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", metrics.Handler())

	return &Server{
		logger:      logger,
		httpAddr:    fmt.Sprintf("%s:%d", cfg.HTTPHost, cfg.HTTPPort),
		metricsAddr: fmt.Sprintf("%s:%d", cfg.MetricsHost, cfg.MetricsPort),
		httpServer:  &http.Server{Handler: engine},
		metricsServer: &http.Server{
			Handler: metricsMux,
		},
	}
}

func ginMode(environment string) string {
	if environment == "production" {
		return gin.ReleaseMode
	}
	return gin.DebugMode
}

// Listen binds both TCP listeners. It's synchronous and fast — safe to
// call right before Serve, and the only step tests need in order to learn
// the real bound address when a config port of 0 was used.
func (s *Server) Listen() error {
	var err error
	if s.httpListener, err = net.Listen("tcp", s.httpAddr); err != nil {
		return fmt.Errorf("httpserver: bind public listener on %s: %w", s.httpAddr, err)
	}
	if s.metricsListener, err = net.Listen("tcp", s.metricsAddr); err != nil {
		return fmt.Errorf("httpserver: bind metrics listener on %s: %w", s.metricsAddr, err)
	}
	return nil
}

// Serve blocks, running both listeners' accept loops until either fails or
// Shutdown is called. Call Listen first.
func (s *Server) Serve() error {
	errCh := make(chan error, 2)
	go func() { errCh <- s.httpServer.Serve(s.httpListener) }()
	go func() { errCh <- s.metricsServer.Serve(s.metricsListener) }()

	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown drains in-flight requests on both servers within ctx's
// deadline, then closes their listeners.
func (s *Server) Shutdown(ctx context.Context) error {
	httpErr := s.httpServer.Shutdown(ctx)
	metricsErr := s.metricsServer.Shutdown(ctx)
	return errors.Join(httpErr, metricsErr)
}

// HTTPAddr returns the actual bound address of the public API listener —
// only meaningful after Listen has succeeded.
func (s *Server) HTTPAddr() string {
	if s.httpListener == nil {
		return s.httpAddr
	}
	return s.httpListener.Addr().String()
}

// MetricsAddr returns the actual bound address of the metrics listener —
// only meaningful after Listen has succeeded.
func (s *Server) MetricsAddr() string {
	if s.metricsListener == nil {
		return s.metricsAddr
	}
	return s.metricsListener.Addr().String()
}
