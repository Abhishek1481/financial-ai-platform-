// Package ratelimit gates request rate per client via a token-bucket
// limiter (golang.org/x/time/rate, the standard library-adjacent
// implementation — no reason to hand-roll one) keyed per client rather
// than one limiter for the whole process, so one noisy caller can't starve
// everyone else's budget.
package ratelimit

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"golang.org/x/time/rate"
)

var rejectionsTotal = promauto.NewCounter(prometheus.CounterOpts{
	Name: "gateway_rate_limit_rejections_total",
	Help: "Total requests rejected with 429 for exceeding their client's rate limit.",
})

// Limiter holds one token bucket per key, created lazily on first use.
// The map grows by one entry per distinct key ever seen and is never
// evicted — acceptable at this app's scale (keys are authenticated user
// IDs, or client IPs for the handful of pre-auth routes), the same
// "temporary but real" tradeoff as MemoryCache; a Redis-backed limiter
// with its own TTL is the natural upgrade once Phase 16 wires Redis up
// for other reasons anyway.
type Limiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	rps      rate.Limit
	burst    int
}

func New(requestsPerSecond float64, burst int) *Limiter {
	return &Limiter{
		limiters: make(map[string]*rate.Limiter),
		rps:      rate.Limit(requestsPerSecond),
		burst:    burst,
	}
}

// Allow reports whether the caller identified by key may proceed right
// now, consuming one token if so.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	lim, ok := l.limiters[key]
	if !ok {
		lim = rate.NewLimiter(l.rps, l.burst)
		l.limiters[key] = lim
	}
	l.mu.Unlock()
	return lim.Allow()
}

// Middleware aborts with 429 when Allow(keyFunc(c)) is false. keyFunc is
// the caller's choice of client identity — internal/httpserver/routes.go
// uses the authenticated user ID when present, falling back to remote IP
// for the pre-auth routes (register/login) that have no user ID yet.
func (l *Limiter) Middleware(keyFunc func(c *gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !l.Allow(keyFunc(c)) {
			rejectionsTotal.Inc()
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Next()
	}
}
