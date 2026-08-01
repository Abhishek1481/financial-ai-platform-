// Package health separates liveness from readiness: liveness ("is the
// process up") never depends on anything external, readiness ("can this
// instance actually serve traffic") does. Kubernetes treats them
// differently — a failed liveness probe restarts the pod, a failed
// readiness probe just pulls it out of the Service's endpoint list — so
// conflating them causes restart loops when a downstream dependency (not
// the process itself) is unhealthy.
package health

import (
	"context"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
)

// Checker reports whether a dependency this instance relies on is
// currently reachable. Returning an error marks the instance not-ready.
type Checker func(ctx context.Context) error

// Readiness aggregates named checks. No checks are registered in Phase 4 —
// Phase 5 registers a Postgres check, Phase 6 an ml-service gRPC check,
// Phase 13 a Redis check — each phase adds a Register call here rather
// than redesigning the readiness endpoint.
type Readiness struct {
	mu     sync.RWMutex
	checks map[string]Checker
}

func NewReadiness() *Readiness {
	return &Readiness{checks: make(map[string]Checker)}
}

func (r *Readiness) Register(name string, check Checker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks[name] = check
}

// Handler returns 200 with each check's status when every registered check
// passes, or 503 naming which one(s) failed. With zero checks registered
// (Phase 4's state) it always returns 200 — an instance with no known
// dependencies is trivially ready.
func (r *Readiness) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		r.mu.RLock()
		checks := make(map[string]Checker, len(r.checks))
		for name, check := range r.checks {
			checks[name] = check
		}
		r.mu.RUnlock()

		results := make(gin.H, len(checks))
		ready := true
		for name, check := range checks {
			if err := check(c.Request.Context()); err != nil {
				results[name] = err.Error()
				ready = false
			} else {
				results[name] = "ok"
			}
		}

		status := http.StatusOK
		overall := "ready"
		if !ready {
			status = http.StatusServiceUnavailable
			overall = "not_ready"
		}
		c.JSON(status, gin.H{"status": overall, "checks": results})
	}
}
