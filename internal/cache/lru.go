package cache

import (
	"context"
	"time"

	"github.com/maypok86/otter"
)

// CacheStats provides metrics about cache performance
type CacheStats struct {
	Hits      int64
	Misses    int64
	Evictions int64
	Size      int
	MaxSize   int
	HitRate   float64
}

// Cache wraps otter with a context-aware interface
type Cache[K comparable, V any] struct {
	cache   otter.CacheWithVariableTTL[K, V]
	maxSize int
}

// NewCache creates a new LRU cache with TTL support
func NewCache[K comparable, V any](maxSize int, _ time.Duration) *Cache[K, V] {
	// The default TTL parameter is ignored because otter.WithVariableTTL takes per-item TTL,
	// but keeping the parameter signature matches previous invocations if needed.
	c, err := otter.MustBuilder[K, V](maxSize).CollectStats().WithVariableTTL().Build()
	if err != nil {
		panic(err)
	}

	return &Cache[K, V]{
		cache:   c,
		maxSize: maxSize,
	}
}

// Get retrieves a value from the cache
func (c *Cache[K, V]) Get(ctx context.Context, key K) (V, bool) {
	return c.cache.Get(key)
}

// Set adds or updates a value in the cache
func (c *Cache[K, V]) Set(ctx context.Context, key K, value V, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = time.Hour * 87600 // 10 years
	}
	c.cache.Set(key, value, ttl)

	return nil
}

// Delete removes a value from the cache
func (c *Cache[K, V]) Delete(ctx context.Context, key K) error {
	c.cache.Delete(key)

	return nil
}

// Clear removes all entries from the cache
func (c *Cache[K, V]) Clear(ctx context.Context) error {
	c.cache.Clear()

	return nil
}

// Stats returns cache performance metrics
func (c *Cache[K, V]) Stats() CacheStats {
	stats := c.cache.Stats()
	hits := stats.Hits()
	misses := stats.Misses()
	total := hits + misses
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	return CacheStats{
		Hits:      hits,
		Misses:    misses,
		Evictions: stats.EvictedCount(),
		Size:      c.cache.Size(),
		MaxSize:   c.maxSize,
		HitRate:   hitRate,
	}
}
