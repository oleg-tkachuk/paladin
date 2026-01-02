package middleware

import (
	"time"

	"paladin/internal/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func isTechPath(path string) bool {
	switch path {
	case "/health/livez", "/health/readyz", "/metrics", "/version":
		return true
	default:
		return false
	}
}

func RequestLogger(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		dur := time.Since(start)
		status := c.Writer.Status()

		rid := utils.RequestIDFromContext(c.Request.Context(), "")
		tenant := utils.TenantIDFromContext(c.Request.Context(), "")

		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.Int("status", status),
			zap.Duration("latency", dur),
			zap.String("ip", c.ClientIP()),
		}
		if rid != "" {
			fields = append(fields, zap.String("request_id", rid))
		}

		if tenant != "" {
			fields = append(fields, zap.String("tenant_id", tenant))
		}

		if isTechPath(path) {
			log.Debug("HTTP request", fields...)

			return
		}

		if status >= 500 {
			log.Error("HTTP request", fields...)

			return
		}

		if status >= 400 {
			log.Warn("HTTP request", fields...)

			return
		}

		log.Info("HTTP request", fields...)
	}
}
