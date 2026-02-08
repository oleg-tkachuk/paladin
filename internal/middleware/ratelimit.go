package middleware

import (
	"net/http"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// RateLimiterConfig defines the rate and burst for the limiter.
type RateLimiterConfig struct {
	Rate  float64
	Burst int
}

// rateLimiterEntry tracks a limiter and its last access time
type rateLimiterEntry struct {
	limiter    *rate.Limiter
	lastAccess time.Time
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
	// Try read lock first for common case
	rl.mu.RLock()
	entry, exists := rl.visitors[tenantID]
	if exists {
		rl.mu.RUnlock()
		// Update last access (write lock needed)
		rl.mu.Lock()
		entry.lastAccess = time.Now()
		rl.mu.Unlock()
		return entry.limiter
	}
	rl.mu.RUnlock()

	// Need to create new limiter
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Double-check after acquiring write lock
	if entry, exists := rl.visitors[tenantID]; exists {
		entry.lastAccess = time.Now()
		return entry.limiter
	}

	// Evict oldest if at capacity
	if len(rl.visitors) >= rl.maxEntries {
		rl.evictOldest()
	}

	// Create new limiter
	entry = &rateLimiterEntry{
		limiter:    rate.NewLimiter(rl.rate, rl.burst),
		lastAccess: time.Now(),
	}
	rl.visitors[tenantID] = entry

	return entry.limiter
}

// evictOldest removes the least recently used limiter
func (rl *TenantRateLimiter) evictOldest() {
	var oldestTenant string
	var oldestTime time.Time

	for tenantID, entry := range rl.visitors {
		if oldestTenant == "" || entry.lastAccess.Before(oldestTime) {
			oldestTenant = tenantID
			oldestTime = entry.lastAccess
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

	cutoff := time.Now().Add(-rl.cleanupTTL)
	for tenantID, entry := range rl.visitors {
		if entry.lastAccess.Before(cutoff) {
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

// RateLimitMiddleware enforces rate limits per tenant.
func RateLimitMiddleware(cfg *config.Config) gin.HandlerFunc {
	limit := rate.Limit(cfg.RateLimit.RequestsPerSecond)
	burst := cfg.RateLimit.Burst
	maxTenants := cfg.RateLimit.MaxTenants
	cleanupTTL := cfg.RateLimit.CleanupTTL
	cleanupInterval := cfg.RateLimit.CleanupInterval

	rl := NewTenantRateLimiter(limit, burst, maxTenants, cleanupTTL, cleanupInterval)

	return func(c *gin.Context) {
		tenantID := c.GetHeader("X-Tenant-ID") // Or derive from auth
		if tenantID == "" {
			tenantID = "default"
		}

		limiter := rl.GetLimiter(tenantID)
		if !limiter.Allow() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate_limit_exceeded",
			})
			return
		}

		c.Next()
	}
}
