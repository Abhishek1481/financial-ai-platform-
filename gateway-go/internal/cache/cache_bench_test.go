package cache

import (
	"strconv"
	"testing"
	"time"
)

func BenchmarkMemoryCache_Set(b *testing.B) {
	c := NewMemoryCache()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Set("key", "value", time.Minute)
	}
}

func BenchmarkMemoryCache_Get_Hit(b *testing.B) {
	c := NewMemoryCache()
	c.Set("key", "value", time.Minute)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Get("key")
	}
}

func BenchmarkMemoryCache_Get_Miss(b *testing.B) {
	c := NewMemoryCache()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Get("no-such-key")
	}
}

// Simulates the real access pattern behind GET /api/v1/search's caching
// (handlers.SearchHandlers.Search): many distinct cache keys (one per
// distinct query+mode+top_k+filter combination), not one hot key.
func BenchmarkMemoryCache_ManyKeys(b *testing.B) {
	c := NewMemoryCache()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := "search:" + strconv.Itoa(i%1000)
		c.Set(key, "value", time.Minute)
		c.Get(key)
	}
}
