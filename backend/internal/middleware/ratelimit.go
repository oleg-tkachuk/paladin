package middleware

import (
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiterConfig defines the rate and burst for the limiter.
type RateLimiterConfig struct {
	Rate  float64
	Burst int
}

// rateLimiterEntry tracks a limiter and its last access time.
// lastAccess is atomic (UnixNano) so the hot path can bump it without
// upgrading to the write lock — the old RUnlock→Lock upgrade opened a
// window where a concurrent eviction made the bump land on an entry
// that was no longer in the map, skewing LRU decisions under load.
type rateLimiterEntry struct {
	limiter    *rate.Limiter
	lastAccess atomic.Int64
}

func (e *rateLimiterEntry) touch() {
	e.lastAccess.Store(time.Now().UnixNano())
}

// TenantRateLimiter manages rate limiters per tenant with memory bounds.
type TenantRateLimiter struct {
	visitors   map[string]*rateLimiterEntry
	mu         sync.RWMutex
	rate       rate.Limit
	burst      int
	maxEntries int
	cleanupTTL time.Duration
}

// NewTenantRateLimiter creates a new rate limiter manager with memory bounds.
func NewTenantRateLimiter(r rate.Limit, b int, maxEntries int, cleanupTTL, cleanupInterval time.Duration) *TenantRateLimiter {
	rl := &TenantRateLimiter{
		visitors:   make(map[string]*rateLimiterEntry),
		rate:       r,
		burst:      b,
		maxEntries: maxEntries,
		cleanupTTL: cleanupTTL,
	}

	// Start cleanup goroutine
	go rl.periodicCleanup(cleanupInterval)

	return rl
}

// GetLimiter returns or creates a limiter for a given tenant.
func (rl *TenantRateLimiter) GetLimiter(tenantID string) *rate.Limiter {
	// Hot path: read lock only; the access timestamp is atomic so no
	// lock upgrade is needed.
	rl.mu.RLock()
	entry, exists := rl.visitors[tenantID]
	rl.mu.RUnlock()
	if exists {
		entry.touch()
		return entry.limiter
	}

	// Need to create new limiter
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Double-check after acquiring write lock
	if entry, exists = rl.visitors[tenantID]; exists {
		entry.touch()
		return entry.limiter
	}

	// Evict oldest if at capacity
	if len(rl.visitors) >= rl.maxEntries {
		rl.evictOldest()
	}

	// Create new limiter
	entry = &rateLimiterEntry{
		limiter: rate.NewLimiter(rl.rate, rl.burst),
	}
	entry.touch()
	rl.visitors[tenantID] = entry

	return entry.limiter
}

// evictOldest removes the least recently used limiter
func (rl *TenantRateLimiter) evictOldest() {
	var oldestTenant string
	var oldestNanos int64

	for tenantID, entry := range rl.visitors {
		nanos := entry.lastAccess.Load()
		if oldestTenant == "" || nanos < oldestNanos {
			oldestTenant = tenantID
			oldestNanos = nanos
		}
	}

	if oldestTenant != "" {
		delete(rl.visitors, oldestTenant)
	}
}

// periodicCleanup removes stale limiters every interval.
func (rl *TenantRateLimiter) periodicCleanup(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		rl.cleanup()
	}
}

// cleanup removes limiters that haven't been accessed recently
func (rl *TenantRateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := time.Now().Add(-rl.cleanupTTL).UnixNano()
	for tenantID, entry := range rl.visitors {
		if entry.lastAccess.Load() < cutoff {
			delete(rl.visitors, tenantID)
		}
	}
}

// Stats returns current limiter statistics
func (rl *TenantRateLimiter) Stats() map[string]interface{} {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	return map[string]interface{}{
		"active_limiters": len(rl.visitors),
		"max_entries":     rl.maxEntries,
	}
}
