package conversation

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestRedisStore points a real go-redis client at miniredis — a
// pure-Go, in-process Redis server — the same "genuinely tested without
// Docker" approach as internal/cache's RedisCache tests.
func newTestRedisStore(t *testing.T) *RedisStore {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { client.Close() })
	return NewRedisStore(client, time.Hour)
}

func TestRedisStore_HistoryOnUnknownSessionIsEmpty(t *testing.T) {
	store := newTestRedisStore(t)

	history, err := store.History(context.Background(), "no-such-session")
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history) != 0 {
		t.Errorf("history = %v, want empty", history)
	}
}

func TestRedisStore_AppendThenHistoryRoundTrips(t *testing.T) {
	store := newTestRedisStore(t)
	ctx := context.Background()

	if err := store.AppendTurns(ctx, "session-1", Turn{Role: RoleUser, Content: "hi"}); err != nil {
		t.Fatalf("AppendTurns() error = %v", err)
	}
	if err := store.AppendTurns(ctx, "session-1", Turn{Role: RoleAssistant, Content: "hello"}); err != nil {
		t.Fatalf("AppendTurns() error = %v", err)
	}

	history, err := store.History(ctx, "session-1")
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history) != 2 || history[0].Content != "hi" || history[1].Content != "hello" {
		t.Errorf("unexpected history: %+v", history)
	}
	if history[0].Role != RoleUser || history[1].Role != RoleAssistant {
		t.Errorf("roles not preserved across JSON round-trip: %+v", history)
	}
}

func TestRedisStore_SessionsAreIsolated(t *testing.T) {
	store := newTestRedisStore(t)
	ctx := context.Background()

	_ = store.AppendTurns(ctx, "session-a", Turn{Role: RoleUser, Content: "a-turn"})
	_ = store.AppendTurns(ctx, "session-b", Turn{Role: RoleUser, Content: "b-turn"})

	historyA, _ := store.History(ctx, "session-a")
	historyB, _ := store.History(ctx, "session-b")

	if len(historyA) != 1 || historyA[0].Content != "a-turn" {
		t.Errorf("session-a history = %+v", historyA)
	}
	if len(historyB) != 1 || historyB[0].Content != "b-turn" {
		t.Errorf("session-b history = %+v", historyB)
	}
}

func TestRedisStore_CapsHistoryAtMaxTurnsPerSession(t *testing.T) {
	store := newTestRedisStore(t)
	ctx := context.Background()

	for i := 0; i < maxTurnsPerSession+5; i++ {
		if err := store.AppendTurns(ctx, "session-1", Turn{Role: RoleUser, Content: "turn"}); err != nil {
			t.Fatalf("AppendTurns() error = %v", err)
		}
	}

	history, err := store.History(ctx, "session-1")
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history) != maxTurnsPerSession {
		t.Errorf("history length = %d, want %d", len(history), maxTurnsPerSession)
	}
}
