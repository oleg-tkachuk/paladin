package middleware

import (
	"paladin/internal/logger"
	"paladin/internal/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func ContextLogger(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := utils.RequestIDFromContext(c.Request.Context(), "")

		l := log
		if rid != "" {
			l = l.With(zap.String("request_id", rid))
		}

		// Attach to context
		ctx := logger.WithContext(c.Request.Context(), l)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}
