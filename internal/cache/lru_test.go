package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLRUCache(t *testing.T) {
	ctx := context.Background()

	// Create a fast-expiring cache for tests
	c := cache.NewCache[string, string](10, 50*time.Millisecond)

	// Test Set and Get
	err := c.Set(ctx, "key1", "value1", 0)
	require.NoError(t, err)

	val, ok := c.Get(ctx, "key1")
	assert.True(t, ok)
	assert.Equal(t, "value1", val)

	// Test Miss
	_, ok = c.Get(ctx, "missing_key")
	assert.False(t, ok)

	// Test Stats
	stats := c.Stats()
	assert.Equal(t, int64(1), stats.Hits)
	assert.Equal(t, int64(1), stats.Misses)
	assert.Equal(t, 1, stats.Size)
	assert.Equal(t, 10, stats.MaxSize)
	assert.InDelta(t, 0.5, stats.HitRate, 0.0001) // 1 hit / 2 total

	// Test Delete
	err = c.Delete(ctx, "key1")
	require.NoError(t, err)
	_, ok = c.Get(ctx, "key1")
	assert.False(t, ok)

	// Test Clear
	_ = c.Set(ctx, "key2", "value2", 0)
	_ = c.Set(ctx, "key3", "value3", 0)
	err = c.Clear(ctx)
	require.NoError(t, err)

	statsAfterClear := c.Stats()
	assert.Equal(t, 0, statsAfterClear.Size)
	assert.Equal(t, int64(0), statsAfterClear.Hits)
	assert.Equal(t, int64(0), statsAfterClear.Misses)

	// Test Expiration
	_ = c.Set(ctx, "expiring_key", "expiring_val", time.Second)
	val, ok = c.Get(ctx, "expiring_key")
	assert.True(t, ok)
	assert.Equal(t, "expiring_val", val)

	time.Sleep(2 * time.Second) // Wait for expiration

	_, ok = c.Get(ctx, "expiring_key")
	assert.False(t, ok, "key should have expired")
}
