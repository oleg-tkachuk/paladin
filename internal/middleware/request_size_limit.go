package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequestSizeLimitMiddleware limits the maximum size of request bodies
func RequestSizeLimitMiddleware(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Set max bytes reader to prevent large payloads
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)

		c.Next()

		// Check if the request was too large
		if c.Errors.Last() != nil {
			if c.Errors.Last().Err == http.ErrHandlerTimeout {
				c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
					"error": gin.H{
						"code":    "request_too_large",
						"message": "Request body exceeds maximum allowed size",
					},
				})
				return
			}
		}
	}
}
