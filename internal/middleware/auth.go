package middleware

import (
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/utils"

	"github.com/gin-gonic/gin"
)

// EnforceTenant rejects requests that require a tenant context but don't have one.
// The tenant context is typically established by the RequestID middleware (from headers).
func EnforceTenant(cfg config.Security) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		derivedTenant := utils.TenantIDFromContext(ctx, "")

		// Check if request payload/params has a conflicting tenant_id
		reqTenant := c.Param("tenant_id") // if route is /v1/:tenant_id/...
		if reqTenant != "" && derivedTenant != "" && reqTenant != derivedTenant {
			if cfg.RejectTenantMismatch {
				c.AbortWithStatusJSON(errors.MapToHTTP(c.Request.Context(),
					errors.Forbidden("tenant mismatch", nil)))
				return
			}
		}

		// Skip tenant enforcement for health and version endpoints
		path := c.Request.URL.Path
		if path == "/health/livez" || path == "/health/readyz" || path == "/health/startupz" || path == "/version" ||
			path == "/v1/health/livez" || path == "/v1/health/readyz" || path == "/v1/health/startupz" || path == "/v1/version" ||
			path == "/v1/admin/config" || path == "/admin/config" {
			c.Next()
			return
		}

		if derivedTenant == "" {
			c.AbortWithStatusJSON(errors.MapToHTTP(c.Request.Context(),
				errors.Unauthorized("missing tenant context", nil)))
			return
		}

		c.Next()
	}
}
