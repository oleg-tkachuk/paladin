package cache

import (
	"context"
	"sync/atomic"
	"time"

	expirable "github.com/go-pkgz/expirable-cache/v3"
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

// Cache wraps expirable-cache with a context-aware interface
type Cache[K comparable, V any] struct {
	cache   expirable.Cache[K, V]
	maxSize int
	hits    atomic.Int64
	misses  atomic.Int64
}

// NewCache creates a new LRU cache with TTL support
func NewCache[K comparable, V any](maxSize int, ttl time.Duration) *Cache[K, V] {
	return &Cache[K, V]{
		cache:   expirable.NewCache[K, V]().WithMaxKeys(maxSize).WithTTL(ttl),
		maxSize: maxSize,
	}
}

// Get retrieves a value from the cache
func (c *Cache[K, V]) Get(ctx context.Context, key K) (V, bool) {
	val, ok := c.cache.Get(key)
	if ok {
		c.hits.Add(1)
	} else {
		c.misses.Add(1)
	}
	return val, ok
}

// Set adds or updates a value in the cache
func (c *Cache[K, V]) Set(ctx context.Context, key K, value V, ttl time.Duration) error {
	c.cache.Set(key, value, ttl)
	return nil
}

// Delete removes a value from the cache
func (c *Cache[K, V]) Delete(ctx context.Context, key K) error {
	c.cache.Invalidate(key)
	return nil
}

// Clear removes all entries from the cache
func (c *Cache[K, V]) Clear(ctx context.Context) error {
	c.cache.Purge()
	c.hits.Store(0)
	c.misses.Store(0)
	return nil
}

// Stats returns cache performance metrics
func (c *Cache[K, V]) Stats() CacheStats {
	stats := c.cache.Stat()
	keys := c.cache.Keys()
	hits := c.hits.Load()
	misses := c.misses.Load()
	total := hits + misses
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	return CacheStats{
		Hits:      hits,
		Misses:    misses,
		Evictions: int64(stats.Evicted),
		Size:      len(keys),
		MaxSize:   c.maxSize,
		HitRate:   hitRate,
	}
}
