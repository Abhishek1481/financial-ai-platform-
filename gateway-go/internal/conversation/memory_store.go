package conversation

import (
	"context"
	"sync"
	"time"
)

// maxTurnsPerSession bounds how much history a session accumulates —
// without a cap, a long-running conversation would grow the prompt sent to
// ml-service on every turn until it blew past the LLM's context window.
// Trimming to the most recent turns (oldest dropped first) keeps recent
// context, which is what a follow-up question actually needs.
const maxTurnsPerSession = 20

// MemoryStore is a Store backed by an in-process map — lost on restart,
// never shared across replicas, the same "temporary but real" tradeoff as
// auth.MemoryUserRepository and ingestion's in-memory repositories (see
// gateway-go/README.md's design-decisions section for why that's
// deliberate rather than an oversight).
type MemoryStore struct {
	mu           sync.Mutex
	turnsBy      map[string][]Turn
	lastActivity map[string]time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		turnsBy:      make(map[string][]Turn),
		lastActivity: make(map[string]time.Time),
	}
}

func (s *MemoryStore) AppendTurns(ctx context.Context, sessionID string, turns ...Turn) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	updated := append(s.turnsBy[sessionID], turns...)
	if len(updated) > maxTurnsPerSession {
		updated = updated[len(updated)-maxTurnsPerSession:]
	}
	s.turnsBy[sessionID] = updated
	s.lastActivity[sessionID] = time.Now()
	return nil
}

func (s *MemoryStore) History(ctx context.Context, sessionID string) ([]Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored := s.turnsBy[sessionID]
	history := make([]Turn, len(stored))
	copy(history, stored)
	return history, nil
}

// PruneOlderThan deletes every session whose most recent AppendTurns call
// was before cutoff, returning how many were removed. Intended to run
// periodically (see internal/scheduler and cmd/gateway/main.go) — without
// this, an abandoned session (a user who never returns) would sit in
// memory for the life of the process, an unbounded leak in a long-running
// deployment even though any single session is capped at
// maxTurnsPerSession.
func (s *MemoryStore) PruneOlderThan(cutoff time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	pruned := 0
	for sessionID, last := range s.lastActivity {
		if last.Before(cutoff) {
			delete(s.turnsBy, sessionID)
			delete(s.lastActivity, sessionID)
			pruned++
		}
	}
	return pruned
}
