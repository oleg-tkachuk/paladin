package middleware

import (
	"paladin/internal/auth"
	"paladin/internal/config"
	"paladin/internal/errors"
	"paladin/internal/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Auth middleware extracts and validates authentication.
// In OIDC mode, it validates JWT tokens and extracts claims.
// In disabled mode, it uses the configured dev principal.
func Auth(authorizer auth.Authorizer, cfg config.Security, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Authenticate request
		principal, err := authorizer.Authenticate(c.Request)
		if err != nil {
			log.Warn("authentication failed", zap.Error(err))
			c.AbortWithStatusJSON(errors.MapToHTTP(c.Request.Context(),
				errors.Unauthorized("authentication required", err)))
			return
		}

		// Store principal in context
		ctx := auth.WithPrincipal(c.Request.Context(), principal)

		// Store tenant ID for backward compatibility with existing code
		ctx = utils.WithTenantID(ctx, principal.TenantID.String())

		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

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
