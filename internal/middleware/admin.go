package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/errors"
)

const adminKeyPrefix = "Bearer "

// AdminOnly returns a middleware that enforces admin-key authentication.
// When auth is disabled (local dev), all requests pass through.
// Otherwise, the request must supply "Authorization: Bearer <admin_key>".
func AdminOnly(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Auth disabled — allow everything (local/dev environment).
		if !cfg.Auth.Enabled {
			c.Next()
			return
		}

		// No admin key configured — the admin endpoints are inaccessible.
		if cfg.Auth.AdminKey == "" {
			c.AbortWithStatusJSON(errors.MapToHTTP(c.Request.Context(),
				errors.Forbidden("admin key not configured; admin endpoints are disabled", nil)))

			return
		}

		authHeader := c.GetHeader("Authorization")
		if !strings.HasPrefix(authHeader, adminKeyPrefix) ||
			strings.TrimPrefix(authHeader, adminKeyPrefix) != cfg.Auth.AdminKey {
			c.AbortWithStatusJSON(errors.MapToHTTP(c.Request.Context(),
				errors.Forbidden("admin credentials required", nil)))

			return
		}

		c.Next()
	}
}
