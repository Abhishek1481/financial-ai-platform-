package cache

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestRedisCache points a real go-redis client at miniredis — a
// pure-Go, in-process implementation of the Redis protocol — so these
// tests exercise real client/server wire behavior without needing an
// actual Redis binary or Docker (neither is available in every dev/CI
// environment this test suite runs in). Returns the miniredis server too,
// since TTL tests need its FastForward to advance its clock deterministically
// rather than sleeping in real time.
func newTestRedisCache(t *testing.T) (*RedisCache, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t) // t.Cleanup-registered shutdown
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { client.Close() })
	return NewRedisCache(client), server
}

func TestRedisCache_MissOnUnknownKey(t *testing.T) {
	c, _ := newTestRedisCache(t)

	if _, ok := c.Get("no-such-key"); ok {
		t.Error("expected a miss on an unknown key")
	}
}

func TestRedisCache_SetThenGetRoundTrips(t *testing.T) {
	c, _ := newTestRedisCache(t)
	c.Set("key", "value", time.Minute)

	value, ok := c.Get("key")
	if !ok || value != "value" {
		t.Errorf("Get() = (%q, %v), want (\"value\", true)", value, ok)
	}
}

func TestRedisCache_ExpiredEntryIsAMiss(t *testing.T) {
	c, server := newTestRedisCache(t)
	c.Set("key", "value", time.Second)
	server.FastForward(2 * time.Second) // advance miniredis's clock deterministically, not a real sleep

	if _, ok := c.Get("key"); ok {
		t.Error("expected an expired entry to be a miss")
	}
}

func TestRedisCache_SetOverwritesExistingKey(t *testing.T) {
	c, _ := newTestRedisCache(t)
	c.Set("key", "first", time.Minute)
	c.Set("key", "second", time.Minute)

	value, ok := c.Get("key")
	if !ok || value != "second" {
		t.Errorf("Get() = (%q, %v), want (\"second\", true)", value, ok)
	}
}
