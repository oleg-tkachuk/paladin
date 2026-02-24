package middleware

import (
	"time"

	"github.com/oleg-tkachuk/paladin/internal/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func isHealthPath(path string) bool {
	return path == "/health/livez" || path == "/health/readyz" || path == "/health/startupz" ||
		path == "/v1/health/livez" || path == "/v1/health/readyz" || path == "/v1/health/startupz"
}

func isTechPath(path string) bool {
	switch path {
	case "/health/livez", "/health/readyz", "/health/startupz", "/metrics", "/version",
		"/v1/health/livez", "/v1/health/readyz", "/v1/health/startupz", "/v1/metrics", "/v1/version":
		return true
	default:
		return false
	}
}

func RequestLogger(log *zap.Logger, logProbes bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		if isHealthPath(path) && !logProbes {
			return
		}

		dur := time.Since(start)
		status := c.Writer.Status()

		rid := utils.RequestIDFromContext(c.Request.Context(), "")
		tenant := utils.TenantIDFromContext(c.Request.Context(), "")

		fields := []zap.Field{
			zap.String("http.request.method", c.Request.Method),
			zap.String("url.path", path),
			zap.Int("http.response.status_code", status),
			zap.Duration("duration", dur),
			zap.String("client.address", c.ClientIP()),
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
