package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCache is the Cache this package's doc comment promises MemoryCache
// slots out for once Redis is available — Phase 16's Docker Compose stack
// runs a real `redis` container, so this is genuinely wired and testable
// (see redis_cache_test.go, which points a real go-redis client at
// miniredis — a pure-Go, in-process Redis server, not a mock — rather than
// requiring a live Redis for `go test` to pass) even though this
// environment itself has no Docker daemon to run the Compose stack's own
// `redis` container against.
type RedisCache struct {
	client *redis.Client
}

func NewRedisCache(client *redis.Client) *RedisCache {
	return &RedisCache{client: client}
}

// redis.Nil (unset key) and any other Redis error (connection drop,
// timeout) both degrade Get to a miss — a cache is never the source of
// truth, so a lookup failure should cost the caller a real request, not
// fail the request outright.
func (c *RedisCache) Get(key string) (string, bool) {
	value, err := c.client.Get(context.Background(), key).Result()
	if err != nil {
		requestsTotal.WithLabelValues("miss").Inc()
		return "", false
	}
	requestsTotal.WithLabelValues("hit").Inc()
	return value, true
}

func (c *RedisCache) Set(key, value string, ttl time.Duration) {
	// Errors are intentionally swallowed here too, for the same reason as
	// Get: a failed Set just means the next Get is a miss, not a request
	// failure.
	_ = c.client.Set(context.Background(), key, value, ttl).Err()
}
