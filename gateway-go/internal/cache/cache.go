// Package cache provides a small TTL key-value cache in front of
// expensive, idempotent gRPC calls (SearchService.Search today).
//
// MemoryCache is a deliberate, temporary stand-in — same "temporary but
// real" tradeoff as auth.MemoryUserRepository and conversation.MemoryStore
// (see gateway-go/README.md's design-decisions section): no Redis
// connection is wired up yet (that lands alongside Docker Compose in
// Phase 16), and every caller depends on the Cache interface, not the
// concrete store, so a RedisCache slots in later without any caller
// changing. Being in-process also means the cache isn't shared across
// replicas — acceptable for a cache (a miss just costs a real request,
// never a correctness bug), which is not true of conversation memory or
// user accounts.
package cache

import (
	"sync"
	"time"
)

type Cache interface {
	// Get returns the cached value and true, or ("", false) on a miss —
	// including an expired entry, which counts as a miss.
	Get(key string) (string, bool)
	Set(key, value string, ttl time.Duration)
}

type entry struct {
	value     string
	expiresAt time.Time
}

// MemoryCache expires entries lazily (checked on Get, not swept on a
// timer) — simplest correct option at the data volumes and TTLs this
// cache runs at (seconds-to-minutes, not hours), and avoids a background
// goroutine whose lifecycle would need to be wired into Server's
// Listen/Serve/Shutdown for no real benefit over a check-on-read.
type MemoryCache struct {
	mu      sync.Mutex
	entries map[string]entry
}

func NewMemoryCache() *MemoryCache {
	return &MemoryCache{entries: make(map[string]entry)}
}

func (c *MemoryCache) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[key]
	if !ok {
		return "", false
	}
	if time.Now().After(e.expiresAt) {
		delete(c.entries, key)
		return "", false
	}
	return e.value, true
}

func (c *MemoryCache) Set(key, value string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = entry{value: value, expiresAt: time.Now().Add(ttl)}
}
