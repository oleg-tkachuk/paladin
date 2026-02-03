package middleware

import (
	"github.com/gin-gonic/gin"
)

// SecurityHeadersMiddleware adds security headers to all HTTP responses
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Prevent MIME type sniffing
		c.Header("X-Content-Type-Options", "nosniff")

		// Prevent clickjacking attacks
		c.Header("X-Frame-Options", "DENY")

		// Enable XSS protection in older browsers
		c.Header("X-XSS-Protection", "1; mode=block")

		// Enforce HTTPS (only send over secure connections)
		// max-age=31536000 = 1 year
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")

		// Restrict resource loading to same origin
		c.Header("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")

		// Control referrer information
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")

		// Prevent browsers from performing certain MIME type sniffing
		c.Header("X-Permitted-Cross-Domain-Policies", "none")

		c.Next()
	}
}
