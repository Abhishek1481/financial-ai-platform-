package cache

import (
	"testing"
	"time"
)

func TestMemoryCache_MissOnUnknownKey(t *testing.T) {
	c := NewMemoryCache()

	if _, ok := c.Get("no-such-key"); ok {
		t.Error("expected a miss on an unknown key")
	}
}

func TestMemoryCache_SetThenGetRoundTrips(t *testing.T) {
	c := NewMemoryCache()
	c.Set("key", "value", time.Minute)

	value, ok := c.Get("key")
	if !ok || value != "value" {
		t.Errorf("Get() = (%q, %v), want (\"value\", true)", value, ok)
	}
}

func TestMemoryCache_ExpiredEntryIsAMiss(t *testing.T) {
	c := NewMemoryCache()
	c.Set("key", "value", -time.Second) // already expired

	if _, ok := c.Get("key"); ok {
		t.Error("expected an expired entry to be a miss")
	}
}

func TestMemoryCache_SetOverwritesExistingKey(t *testing.T) {
	c := NewMemoryCache()
	c.Set("key", "first", time.Minute)
	c.Set("key", "second", time.Minute)

	value, ok := c.Get("key")
	if !ok || value != "second" {
		t.Errorf("Get() = (%q, %v), want (\"second\", true)", value, ok)
	}
}
