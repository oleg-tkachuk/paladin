package capability

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// RevocationLookup is the seam the cache wraps. A persistent store
// implements it; tests substitute a stub. Like Store.IsRevoked, it answers
// for the capability's whole delegation chain.
type RevocationLookup interface {
	IsRevoked(ctx context.Context, id uuid.UUID) (bool, error)
}

// DefaultRevocationCacheTTL bounds how long a cached answer is served. A
// revocation therefore takes effect within this window on every verifier
// that did not perform it.
const DefaultRevocationCacheTTL = 2 * time.Second

// DefaultRevocationCacheEntries bounds the cache. Past it, expired entries
// are dropped and, if that frees nothing, the cache is reset: a flood of
// distinct ids costs extra lookups, never unbounded memory.
const DefaultRevocationCacheEntries = 100_000

// CachedRevocationChecker is a small in-memory cache for IsRevoked answers.
//
// A cached answer about a capability is also an answer about its ancestors,
// so revoking a parent reaches a child's cached "live" entry only when that
// entry expires — within the TTL, like any other revocation seen from
// another process.
//
// Concurrent misses for the same id share one upstream call.
type CachedRevocationChecker struct {
	upstream   RevocationLookup
	ttl        time.Duration
	maxEntries int
	now        func() time.Time

	mu       sync.Mutex
	entries  map[uuid.UUID]cacheEntry
	inflight map[uuid.UUID]*lookupCall
}

type cacheEntry struct {
	revoked bool
	expires time.Time
}

type lookupCall struct {
	done    chan struct{}
	revoked bool
	err     error
}

// CacheOption configures a CachedRevocationChecker.
type CacheOption func(*CachedRevocationChecker)

// WithCacheClock replaces time.Now; tests use it to step through expiry.
func WithCacheClock(now func() time.Time) CacheOption {
	return func(c *CachedRevocationChecker) { c.now = now }
}

// WithMaxEntries bounds the number of cached ids. ≤ 0 keeps the default.
func WithMaxEntries(n int) CacheOption {
	return func(c *CachedRevocationChecker) {
		if n > 0 {
			c.maxEntries = n
		}
	}
}

// NewCachedRevocationChecker wires a cache over an upstream lookup. ttl
// of 0 falls back to DefaultRevocationCacheTTL; ttl < 0 disables caching
// (every call hits upstream — useful for tests).
func NewCachedRevocationChecker(upstream RevocationLookup, ttl time.Duration, opts ...CacheOption) *CachedRevocationChecker {
	if ttl == 0 {
		ttl = DefaultRevocationCacheTTL
	}
	c := &CachedRevocationChecker{
		upstream:   upstream,
		ttl:        ttl,
		maxEntries: DefaultRevocationCacheEntries,
		now:        time.Now,
		entries:    make(map[uuid.UUID]cacheEntry),
		inflight:   make(map[uuid.UUID]*lookupCall),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// IsRevoked serves a fresh cached answer, or asks upstream once on behalf of
// every concurrent caller asking about the same id. An upstream error is
// returned to all of them and is never cached.
func (c *CachedRevocationChecker) IsRevoked(ctx context.Context, id uuid.UUID) (bool, error) {
	if c.ttl < 0 {
		return c.upstream.IsRevoked(ctx, id)
	}

	c.mu.Lock()
	if e, ok := c.entries[id]; ok && e.expires.After(c.now()) {
		c.mu.Unlock()
		return e.revoked, nil
	}
	if call, ok := c.inflight[id]; ok {
		c.mu.Unlock()
		select {
		case <-call.done:
			// The leader's request may have been cancelled or timed out on
			// its own account. That is no answer about the id, so a waiter
			// whose own context is still live asks for itself.
			if call.err != nil && isContextErr(call.err) && ctx.Err() == nil {
				return c.upstream.IsRevoked(ctx, id)
			}
			return call.revoked, call.err
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	call := &lookupCall{done: make(chan struct{})}
	c.inflight[id] = call
	c.mu.Unlock()

	call.revoked, call.err = c.upstream.IsRevoked(ctx, id)

	c.mu.Lock()
	delete(c.inflight, id)
	if call.err == nil {
		c.makeRoomLocked()
		c.entries[id] = cacheEntry{revoked: call.revoked, expires: c.now().Add(c.ttl)}
	}
	c.mu.Unlock()
	close(call.done)
	return call.revoked, call.err
}

// makeRoomLocked keeps the cache under maxEntries.
func (c *CachedRevocationChecker) makeRoomLocked() {
	if len(c.entries) < c.maxEntries {
		return
	}
	c.sweepLocked()
	if len(c.entries) >= c.maxEntries {
		clear(c.entries)
	}
}

// Invalidate drops a single entry — call it from the path that revoked the
// capability so the local cache reflects the new truth immediately. Other
// processes catch up within the TTL.
func (c *CachedRevocationChecker) Invalidate(id uuid.UUID) {
	c.mu.Lock()
	delete(c.entries, id)
	c.mu.Unlock()
}

// Sweep drops every expired entry and returns how many it dropped.
// Optional housekeeping — lookups skip stale entries anyway.
func (c *CachedRevocationChecker) Sweep() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sweepLocked()
}

func (c *CachedRevocationChecker) sweepLocked() int {
	now := c.now()
	dropped := 0
	for id, e := range c.entries {
		if !e.expires.After(now) {
			delete(c.entries, id)
			dropped++
		}
	}
	return dropped
}

func isContextErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
