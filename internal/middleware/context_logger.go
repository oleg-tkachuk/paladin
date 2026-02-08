package middleware

import (
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func ContextLogger(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get logger with potential current context enrichment
		l := logger.FromContext(c.Request.Context())

		rid := utils.RequestIDFromContext(c.Request.Context(), "")
		if rid != "" {
			l = l.With(zap.String("request_id", rid))
		}

		// Update context with enriched logger
		ctx := logger.WithContext(c.Request.Context(), l)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}
