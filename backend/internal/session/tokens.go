package session

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// accessEarlyExpiry is how long before its expiry a cached access token is
// dropped, so a token is never forwarded with seconds left to live.
const accessEarlyExpiry = 30 * time.Second

// maxCachedAccess bounds the cache: past it, expired entries are swept, and
// if none were, the cache starts over. A cache, never the session's state.
const maxCachedAccess = 10000

// accessCache keeps the access tokens minted for each session and audience
// until they near expiry. It is only a cache: losing it, or a replica that
// never had it, costs one more ExchangeAudience.
type accessCache struct {
	mu      sync.Mutex
	entries map[string]cachedAccess
	now     func() time.Time
}

type cachedAccess struct {
	token string
	until time.Time
}

func newAccessCache(now func() time.Time) *accessCache {
	return &accessCache{entries: map[string]cachedAccess{}, now: now}
}

// cacheKey keys a session's token for audience by a hash of its refresh
// token: a tenant switch or a rotation is a new refresh token, so it never
// meets the old one's access token.
func cacheKey(refresh, audience string) string {
	sum := sha256.Sum256([]byte(refresh))
	return hex.EncodeToString(sum[:]) + "|" + audience
}

func (c *accessCache) get(refresh, audience string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[cacheKey(refresh, audience)]
	if !ok || !c.now().Before(e.until) {
		return "", false
	}
	return e.token, true
}

func (c *accessCache) put(refresh, audience, token string, ttl time.Duration) {
	until := c.now().Add(ttl - accessEarlyExpiry)
	if !c.now().Before(until) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= maxCachedAccess {
		c.sweep()
	}
	c.entries[cacheKey(refresh, audience)] = cachedAccess{token: token, until: until}
}

// forget drops every token cached for a session, at logout.
func (c *accessCache) forget(refresh string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, aud := range planeAudience {
		delete(c.entries, cacheKey(refresh, aud))
	}
}

// sweep drops expired entries, or everything when none had expired.
func (c *accessCache) sweep() {
	now := c.now()
	before := len(c.entries)
	for k, e := range c.entries {
		if !now.Before(e.until) {
			delete(c.entries, k)
		}
	}
	if len(c.entries) == before {
		c.entries = map[string]cachedAccess{}
	}
}
