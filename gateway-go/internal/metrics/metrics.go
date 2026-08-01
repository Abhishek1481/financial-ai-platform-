// Package metrics exposes gateway-go's Prometheus instrumentation: Go
// runtime/process metrics, and per-request counters and latency histograms
// captured by Gin middleware.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Go runtime and process collectors (goroutine counts, GC pauses, RSS,
// etc.) are NOT registered here explicitly — the prometheus package's own
// init() already registers them to DefaultRegisterer. Doing it again would
// panic with "duplicate metrics collector registration attempted".

var (
	requestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_http_requests_total",
		Help: "Total HTTP requests handled, labeled by method, route, and status code.",
	}, []string{"method", "route", "status"})

	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "gateway_http_request_duration_seconds",
		Help:    "HTTP request latency in seconds, labeled by method and route.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})
)

// Middleware records request count and latency for every request that
// passes through the Gin engine it's attached to.
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			// No route matched (404) — label as "unmatched" rather than the
			// raw path, which would otherwise let a client generate
			// unbounded label cardinality by hitting random URLs.
			route = "unmatched"
		}
		status := strconv.Itoa(c.Writer.Status())

		requestsTotal.WithLabelValues(c.Request.Method, route, status).Inc()
		requestDuration.WithLabelValues(c.Request.Method, route).Observe(time.Since(start).Seconds())
	}
}

// Handler serves the Prometheus exposition format. Mounted on the internal
// metrics port, never on the public API port (see internal/config).
func Handler() http.Handler {
	return promhttp.Handler()
}
