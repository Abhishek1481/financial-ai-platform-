package conversation

import "context"

// Store is the persistence boundary RAGHandlers depends on for
// multi-turn conversation memory, keyed by the caller-supplied (or
// server-generated) session_id — same Repository-pattern reasoning as
// auth.UserRepository: handlers depend on the interface, and
// MemoryStore is a deliberate, temporary stand-in swapped for a
// Redis/Postgres-backed implementation once one is wired up (Phase 13/16).
type Store interface {
	AppendTurns(ctx context.Context, sessionID string, turns ...Turn) error
	History(ctx context.Context, sessionID string) ([]Turn, error)
}
