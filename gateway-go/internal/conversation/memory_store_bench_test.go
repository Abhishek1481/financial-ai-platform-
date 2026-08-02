package conversation

import (
	"context"
	"strconv"
	"testing"
)

func BenchmarkMemoryStore_AppendTurns(b *testing.B) {
	store := NewMemoryStore()
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.AppendTurns(ctx, "session-1", Turn{Role: RoleUser, Content: "hi"})
	}
}

func BenchmarkMemoryStore_History(b *testing.B) {
	store := NewMemoryStore()
	ctx := context.Background()
	for i := 0; i < maxTurnsPerSession; i++ {
		_ = store.AppendTurns(ctx, "session-1", Turn{Role: RoleUser, Content: "hi"})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = store.History(ctx, "session-1")
	}
}

// Many concurrent sessions (the real shape in production — one per user
// conversation), not one hot session — exercises the same map-growth
// behavior PruneOlderThan later has to scan through.
func BenchmarkMemoryStore_ManySessions(b *testing.B) {
	store := NewMemoryStore()
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sessionID := "session-" + strconv.Itoa(i%10000)
		_ = store.AppendTurns(ctx, sessionID, Turn{Role: RoleUser, Content: "hi"})
	}
}
