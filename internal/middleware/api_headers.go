package middleware

import (
	"github.com/gin-gonic/gin"
)

// APIVersionMiddleware adds API version header to all responses
func APIVersionMiddleware(version string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-API-Version", version)
		c.Next()
	}
}

// RateLimitHeadersMiddleware adds rate limiting headers to responses
// This is a placeholder for future rate limiting implementation
func RateLimitHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// TODO: Implement actual rate limiting logic
		// For now, add static headers to document the feature
		c.Header("X-RateLimit-Limit", "1000")
		c.Header("X-RateLimit-Remaining", "999")
		// X-RateLimit-Reset would be set dynamically based on rate limiter state

		c.Next()
	}
}
