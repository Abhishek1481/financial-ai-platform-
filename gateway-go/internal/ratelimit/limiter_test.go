package ratelimit

import "testing"

func TestLimiter_AllowsUpToBurstThenBlocks(t *testing.T) {
	l := New(0, 2) // 0 refill rate isolates this test to burst behavior

	if !l.Allow("client-1") {
		t.Fatal("first request should be allowed")
	}
	if !l.Allow("client-1") {
		t.Fatal("second request (within burst) should be allowed")
	}
	if l.Allow("client-1") {
		t.Fatal("third request should be blocked once burst is exhausted")
	}
}

func TestLimiter_KeysAreIndependent(t *testing.T) {
	l := New(0, 1)

	if !l.Allow("client-1") {
		t.Fatal("client-1's first request should be allowed")
	}
	if !l.Allow("client-2") {
		t.Fatal("client-2 should have its own budget, independent of client-1")
	}
	if l.Allow("client-1") {
		t.Fatal("client-1 should be blocked after exhausting its own burst")
	}
}
