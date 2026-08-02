// Package scheduler runs a periodic background task — today, pruning
// stale conversation sessions (see cmd/gateway/main.go), but generic
// enough for whatever else needs a "do this every N minutes" job later.
package scheduler

import (
	"context"
	"time"
)

type Task func(ctx context.Context)

// Run invokes task once per interval until ctx is done, then returns —
// same ctx-owns-the-lifecycle shape as ingestion.Service.Start/Stop
// (see cmd/gateway/main.go's workerCtx/cancelWorkers), so callers manage
// this the same way they already manage the ingestion worker pool. Blocks
// the calling goroutine; callers run it with `go scheduler.Run(...)`.
func Run(ctx context.Context, interval time.Duration, task Task) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			task(ctx)
		case <-ctx.Done():
			return
		}
	}
}
