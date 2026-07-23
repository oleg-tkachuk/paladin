package capability

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// RevocationLookup is the seam the cache wraps. A persistent store
// implements it; tests substitute a stub. Pulled out of Store so the
// cache stays decoupled from the persistence package and can sit in
// front of any storage backend in the future (Redis, pgbouncer-fronted
// read replica).
type RevocationLookup interface {
	IsRevoked(ctx context.Context, id uuid.UUID) (bool, error)
}

// CachedRevocationChecker is a small in-memory cache for
// IsRevoked answers. Entries TTL out after the configured period —
// default 2s, matching the BACKLOG SLO for revocation propagation.
//
// Concurrency: a sync.RWMutex protects the map. Hits take the read
// lock; misses upgrade to write to insert. The map is bounded only
// by the natural cardinality of in-flight capability IDs (≤ a few
// thousand at typical scale); a hard size cap can be added later if
// runaway tenants pollute the cache.
type CachedRevocationChecker struct {
	upstream RevocationLookup
	ttl      time.Duration

	mu      sync.RWMutex
	entries map[uuid.UUID]cacheEntry
}

type cacheEntry struct {
	revoked bool
	expires time.Time
}

// NewCachedRevocationChecker wires a cache over an upstream lookup. ttl
// of 0 falls back to 2 seconds; ttl < 0 disables caching (every call
// hits upstream — useful for tests).
func NewCachedRevocationChecker(upstream RevocationLookup, ttl time.Duration) *CachedRevocationChecker {
	if ttl == 0 {
		ttl = 2 * time.Second
	}
	return &CachedRevocationChecker{
		upstream: upstream,
		ttl:      ttl,
		entries:  make(map[uuid.UUID]cacheEntry),
	}
}

// IsRevoked checks the cache, falling through to the upstream on miss
// or expired entry. Cache writes happen under the write lock; the
// double-check pattern avoids two goroutines both upgrading on the
// same key.
func (c *CachedRevocationChecker) IsRevoked(ctx context.Context, id uuid.UUID) (bool, error) {
	if c.ttl < 0 {
		return c.upstream.IsRevoked(ctx, id)
	}

	now := time.Now()

	c.mu.RLock()
	if e, ok := c.entries[id]; ok && e.expires.After(now) {
		c.mu.RUnlock()
		return e.revoked, nil
	}
	c.mu.RUnlock()

	revoked, err := c.upstream.IsRevoked(ctx, id)
	if err != nil {
		return false, err
	}

	c.mu.Lock()
	// Re-check under the write lock so concurrent fillers don't
	// stampede each other's writes (rare but cheap to handle).
	if e, ok := c.entries[id]; !ok || !e.expires.After(now) {
		c.entries[id] = cacheEntry{revoked: revoked, expires: now.Add(c.ttl)}
	}
	c.mu.Unlock()
	return revoked, nil
}

// Invalidate drops a single entry — call from the issuer/admin path
// when a capability is revoked through this process so the local cache
// reflects the new truth immediately. Other pods will catch up at the
// natural TTL.
func (c *CachedRevocationChecker) Invalidate(id uuid.UUID) {
	c.mu.Lock()
	delete(c.entries, id)
	c.mu.Unlock()
}

// Sweep drops every expired entry. Optional housekeeping — the cache
// works fine without it (lookups skip stale entries) but pods with
// long uptime can accumulate cold IDs. The serve worker's housekeeping
// loop calls this on the same cadence as the rest of its sweeps.
func (c *CachedRevocationChecker) Sweep() int {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	dropped := 0
	for id, e := range c.entries {
		if !e.expires.After(now) {
			delete(c.entries, id)
			dropped++
		}
	}
	return dropped
}
