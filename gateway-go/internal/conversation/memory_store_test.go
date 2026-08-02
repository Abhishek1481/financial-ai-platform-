package conversation

import (
	"context"
	"testing"
)

func TestMemoryStore_HistoryOnUnknownSessionIsEmpty(t *testing.T) {
	store := NewMemoryStore()

	history, err := store.History(context.Background(), "no-such-session")
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history) != 0 {
		t.Errorf("history = %v, want empty", history)
	}
}

func TestMemoryStore_AppendThenHistoryRoundTrips(t *testing.T) {
	store := NewMemoryStore()
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
}

func TestMemoryStore_SessionsAreIsolated(t *testing.T) {
	store := NewMemoryStore()
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

func TestMemoryStore_HistoryIsATrimmedCopy(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	_ = store.AppendTurns(ctx, "session-1", Turn{Role: RoleUser, Content: "original"})

	history, _ := store.History(ctx, "session-1")
	history[0].Content = "mutated by caller"

	historyAgain, _ := store.History(ctx, "session-1")
	if historyAgain[0].Content != "original" {
		t.Errorf("stored history was mutated via a returned slice: %+v", historyAgain)
	}
}

func TestMemoryStore_CapsHistoryAtMaxTurnsPerSession(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	for i := 0; i < maxTurnsPerSession+5; i++ {
		_ = store.AppendTurns(ctx, "session-1", Turn{Role: RoleUser, Content: "turn"})
	}

	history, _ := store.History(ctx, "session-1")
	if len(history) != maxTurnsPerSession {
		t.Errorf("history length = %d, want %d", len(history), maxTurnsPerSession)
	}
}
