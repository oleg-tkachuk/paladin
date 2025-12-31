package middleware

import (
	"context"

	"paladin/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const HeaderRequestID = "X-Request-ID"
const HeaderTenantID = "X-Tenant-ID"

func RequestID(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader(HeaderRequestID)
		if rid == "" {
			u, err := uuid.NewV7()
			if err != nil {
				log.Warn("UUIDv7 failed, fallback to UUIDv4", zap.Error(err))

				u = uuid.New()
			}

			rid = u.String()
		}

		tenant := c.GetHeader(HeaderTenantID)
		if tenant == "" {
			tenant = "default"
		}

		c.Writer.Header().Set(HeaderRequestID, rid)

		ctx := context.WithValue(c.Request.Context(), utils.RequestIDKey, rid)
		ctx = context.WithValue(ctx, utils.TraceIDKey, rid) // compat
		ctx = context.WithValue(ctx, utils.TenantIDKey, tenant)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}
