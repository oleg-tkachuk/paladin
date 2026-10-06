package paladin

import (
	"context"
	"errors"
	"sync"
	"time"
)

// DefaultCapabilityRefreshMargin is how long before its expiry a cached
// capability is replaced, so a call does not leave with one that lapses in
// flight.
const DefaultCapabilityRefreshMargin = 30 * time.Second

// ErrNoCapabilityKey is CapabilityCache.Token asked for an empty key.
var ErrNoCapabilityKey = errors.New("paladin: capability cache key is empty")

// MintCapability issues a capability for key — a tenant, a resource prefix,
// whatever the caller's capabilities are scoped by — and returns it with its
// expiry.
type MintCapability func(ctx context.Context, key string) (token string, expires time.Time, err error)

// CapabilityCache keeps one capability per key until DefaultCapabilityRefreshMargin
// (or the margin given) before it expires, for a service that acts for many
// callers and mints each a short-lived capability. Concurrent callers for one
// key wait for a single mint; a mint that fails is not cached, so the next
// call mints again. Feed it to WithCapabilitySource:
//
//	cache := paladin.NewCapabilityCache(mint)
//	paladin.WithCapabilitySource(func(ctx context.Context) string {
//		token, _ := cache.Token(ctx, tenantFrom(ctx))
//		return token
//	})
type CapabilityCache struct {
	mint   MintCapability
	margin time.Duration
	now    func() time.Time

	mu      sync.Mutex
	entries map[string]*capabilityEntry
}

type capabilityEntry struct {
	token   string
	expires time.Time
	// minting is closed when the mint in progress for this key ends; nil
	// once the entry holds a token.
	minting chan struct{}
}

// CapabilityCacheOption configures a CapabilityCache.
type CapabilityCacheOption func(*CapabilityCache)

// WithCapabilityRefreshMargin replaces DefaultCapabilityRefreshMargin.
func WithCapabilityRefreshMargin(margin time.Duration) CapabilityCacheOption {
	return func(c *CapabilityCache) { c.margin = margin }
}

// WithCapabilityCacheClock replaces time.Now, for tests.
func WithCapabilityCacheClock(now func() time.Time) CapabilityCacheOption {
	return func(c *CapabilityCache) { c.now = now }
}

// NewCapabilityCache returns a cache that mints with mint.
func NewCapabilityCache(mint MintCapability, opts ...CapabilityCacheOption) *CapabilityCache {
	c := &CapabilityCache{
		mint: mint, margin: DefaultCapabilityRefreshMargin, now: time.Now,
		entries: map[string]*capabilityEntry{},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Token returns a capability for key, minting one when none is cached or the
// cached one is within the refresh margin of its expiry.
func (c *CapabilityCache) Token(ctx context.Context, key string) (string, error) {
	if key == "" {
		return "", ErrNoCapabilityKey
	}
	for {
		c.mu.Lock()
		e, ok := c.entries[key]
		switch {
		case ok && e.minting == nil && c.now().Add(c.margin).Before(e.expires):
			c.mu.Unlock()
			return e.token, nil
		case ok && e.minting != nil:
			// Another caller is minting for this key: wait, then read again.
			wait := e.minting
			c.mu.Unlock()
			select {
			case <-wait:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			continue
		}
		// Claim the mint before releasing the lock, so the wait above sees it.
		pending := &capabilityEntry{minting: make(chan struct{})}
		c.entries[key] = pending
		c.mu.Unlock()

		token, expires, err := c.mint(ctx, key)

		c.mu.Lock()
		if err != nil {
			delete(c.entries, key)
		} else {
			c.entries[key] = &capabilityEntry{token: token, expires: expires}
		}
		c.mu.Unlock()
		close(pending.minting)
		if err != nil {
			return "", err
		}
		return token, nil
	}
}

// Invalidate drops the capability cached for key, so the next Token mints —
// after the server refused it (revoked, a rotated key). A mint in progress is
// left alone: it is newer than the refusal, and its waiters need it to finish.
func (c *CapabilityCache) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok && e.minting == nil {
		delete(c.entries, key)
	}
}
