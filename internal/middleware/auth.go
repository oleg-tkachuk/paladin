package middleware

import (
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/utils"

	"github.com/gin-gonic/gin"
)

// EnforceTenant rejects requests that require a tenant context but don't have one.
// It also ensures that if a stored object is requested with an explicit tenant mismatch, we block it.
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

		if derivedTenant == "" {
			c.AbortWithStatusJSON(errors.MapToHTTP(c.Request.Context(),
				errors.Unauthorized("missing tenant context", nil)))
			return
		}

		c.Next()
	}
}
