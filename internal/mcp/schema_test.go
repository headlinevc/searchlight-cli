package mcp

import (
	"testing"
	"time"
)

func TestSchemaCache_IsFresh(t *testing.T) {
	cache := SchemaCache{TTL: 24 * time.Hour}

	cases := []struct {
		name   string
		cached *CachedSchema
		want   bool
	}{
		{"nil cache", nil, false},
		{"zero time", &CachedSchema{}, false},
		{"recent", &CachedSchema{ObtainedAt: time.Now().Add(-time.Hour)}, true},
		{"stale (>24h)", &CachedSchema{ObtainedAt: time.Now().Add(-25 * time.Hour)}, false},
	}
	for _, tc := range cases {
		if got := cache.IsFresh(tc.cached); got != tc.want {
			t.Errorf("%s: IsFresh = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSchemaCache_IsFresh_ZeroTTL(t *testing.T) {
	cache := SchemaCache{TTL: 0}
	cs := &CachedSchema{ObtainedAt: time.Now()}
	if cache.IsFresh(cs) {
		t.Error("IsFresh with TTL=0 should always return false")
	}
}
