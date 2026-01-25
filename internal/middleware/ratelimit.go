package middleware

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// RateLimiterConfig defines the rate and burst for the limiter.
type RateLimiterConfig struct {
	Rate  float64
	Burst int
}

// TenantRateLimiter manages rate limiters per tenant.
type TenantRateLimiter struct {
	visitors map[string]*rate.Limiter
	mu       sync.Mutex
	rate     rate.Limit
	burst    int
}

// NewTenantRateLimiter creates a new rate limiter manager.
func NewTenantRateLimiter(r rate.Limit, b int) *TenantRateLimiter {
	return &TenantRateLimiter{
		visitors: make(map[string]*rate.Limiter),
		rate:     r,
		burst:    b,
	}
}

// GetLimiter returns or creates a limiter for a given tenant.
func (rl *TenantRateLimiter) GetLimiter(tenantID string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	limiter, exists := rl.visitors[tenantID]
	if !exists {
		limiter = rate.NewLimiter(rl.rate, rl.burst)
		rl.visitors[tenantID] = limiter
	}

	return limiter
}

// RateLimitMiddleware enforces rate limits per tenant.
func RateLimitMiddleware(limit rate.Limit, burst int) gin.HandlerFunc {
	rl := NewTenantRateLimiter(limit, burst)

	// Clean up old limiters periodically to prevent memory leaks?
	// For production, use Redis or similar. For now, in-memory is fine given requirements.
	// Since we are control plane, tenant count might be manageable.

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
