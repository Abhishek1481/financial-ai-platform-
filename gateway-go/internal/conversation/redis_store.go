package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore is the Store MemoryStore's doc comment promises once Redis
// is available (Phase 16's Docker Compose stack runs a real `redis`
// container). Unlike MemoryStore, it needs no separate prune job: each
// session is a Redis list under key "conversation:<sessionID>", and every
// AppendTurns call refreshes the key's TTL (sessionTTL, a sliding window —
// an active conversation never expires mid-use) via Redis's own native
// expiry rather than a periodic scan. See redis_store_test.go for why this
// is genuinely tested (against miniredis, a pure-Go in-process Redis) even
// though this environment has no Docker daemon to run the Compose stack's
// own `redis` container against.
type RedisStore struct {
	client     *redis.Client
	sessionTTL time.Duration
}

func NewRedisStore(client *redis.Client, sessionTTL time.Duration) *RedisStore {
	return &RedisStore{client: client, sessionTTL: sessionTTL}
}

func sessionKey(sessionID string) string {
	return "conversation:" + sessionID
}

func (s *RedisStore) AppendTurns(ctx context.Context, sessionID string, turns ...Turn) error {
	if len(turns) == 0 {
		return nil
	}
	key := sessionKey(sessionID)

	encoded := make([]any, len(turns))
	for i, turn := range turns {
		raw, err := json.Marshal(turn)
		if err != nil {
			return fmt.Errorf("conversation: encode turn: %w", err)
		}
		encoded[i] = raw
	}

	pipe := s.client.TxPipeline()
	pipe.RPush(ctx, key, encoded...)
	pipe.LTrim(ctx, key, -maxTurnsPerSession, -1)
	pipe.Expire(ctx, key, s.sessionTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("conversation: append turns: %w", err)
	}
	return nil
}

func (s *RedisStore) History(ctx context.Context, sessionID string) ([]Turn, error) {
	raw, err := s.client.LRange(ctx, sessionKey(sessionID), 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("conversation: load history: %w", err)
	}

	turns := make([]Turn, 0, len(raw))
	for _, item := range raw {
		var turn Turn
		if err := json.Unmarshal([]byte(item), &turn); err != nil {
			return nil, fmt.Errorf("conversation: decode turn: %w", err)
		}
		turns = append(turns, turn)
	}
	return turns, nil
}
