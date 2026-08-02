package ratelimit

import (
	"strconv"
	"testing"
)

// A high rate/burst so Allow() never actually blocks — this benchmark is
// about the lock + map-lookup overhead the middleware adds to every
// request, not about measuring how it behaves once a client is throttled
// (limiter_test.go's job).
func BenchmarkLimiter_Allow_SingleKey(b *testing.B) {
	l := New(1e6, 1e6)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Allow("client-1")
	}
}

// The real access pattern behind rate limiting all of /api/v1 (see
// routes.go): many distinct client IPs, each getting its own token
// bucket created lazily on first use.
func BenchmarkLimiter_Allow_ManyKeys(b *testing.B) {
	l := New(1e6, 1e6)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Allow("client-" + strconv.Itoa(i%10000))
	}
}
