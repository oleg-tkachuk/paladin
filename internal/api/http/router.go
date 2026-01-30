package httpapi

import (
	"net/http"
	"sync/atomic"

	"paladin/internal/config"
	"paladin/internal/middleware"
	"paladin/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

type Server struct {
	engine *gin.Engine
}

func NewServer(cfg *config.Config, log *zap.Logger, svc service.ObjectsService, version, commit, buildTime string, hs *service.HealthService, started *atomic.Bool) *Server {
	gin.SetMode(cfg.Server.Mode)
	r := gin.New()

	if len(cfg.Server.HTTP.TrustedProxies) > 0 {
		if err := r.SetTrustedProxies(cfg.Server.HTTP.TrustedProxies); err != nil {
			log.Warn("Failed to set trusted proxies", zap.Error(err))
		}
	} else {
		_ = r.SetTrustedProxies(nil)
	}

	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	r.GET("/version", func(c *gin.Context) {
		c.JSON(http.StatusOK, VersionResponse{
			Service:   "paladin",
			Version:   version,
			Commit:    commit,
			BuildTime: buildTime,
		})
	})

	r.GET("/health/livez", func(c *gin.Context) {
		if cfg.Server.LogProbes {
			log.Debug("Liveness check called")
		}
		// If we are here, the process is running and responsive.
		// In a real app, you might check for deadlocks or other fatal internal state.
		c.JSON(http.StatusOK, gin.H{"status": "alive"})
	})

	r.GET("/health/startupz", func(c *gin.Context) {
		if cfg.Server.LogProbes {
			log.Debug("Startup check called")
		}
		if !started.Load() {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "starting",
				"reason": "initialization_in_progress",
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "started"})
	})

	r.GET("/health/readyz", func(c *gin.Context) {
		if cfg.Server.LogProbes {
			log.Debug("Readiness check called")
		}

		ready, status := hs.CheckReady(c.Request.Context())
		if !ready {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status":       "not_ready",
				"reason":       "dependency_unavailable",
				"dependencies": status,
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status":       "ready",
			"dependencies": status,
		})
	})

	// Use canonical stack for API routes
	middleware.SetupHTTPStack(r, cfg, log)

	// Rate Limiting
	if cfg.RateLimit.RequestsPerSecond > 0 {
		r.Use(middleware.RateLimitMiddleware(rate.Limit(cfg.RateLimit.RequestsPerSecond), cfg.RateLimit.Burst))
	} else {
		// Default fallback if config is missing or zero (safer to have default or just disabled?)
		// Let's assume 10/20 as safe default if not configured, or trust config loader.
		// Given we just added it to config, we should expect it.
		r.Use(middleware.RateLimitMiddleware(10, 20))
	}

	v1 := r.Group("/v1")
	{
		v1.POST("/objects", createObjectHandler(svc))
		v1.GET("/objects/:id", getObjectHandler(svc))
		v1.GET("/objects/:id/meta", getObjectMetaHandler(svc))
		v1.POST("/objects/:id/complete", completeObjectHandler(svc))
		v1.DELETE("/objects/:id", deleteObjectHandler(svc))

		v1.POST("/multipart", initiateMultipartHandler(svc))
		v1.POST("/multipart/:upload_id/parts/:part_number/sign", signPartHandler(svc))
		v1.POST("/multipart/:upload_id/complete", completeMultipartHandler(svc))
		v1.POST("/multipart/:upload_id/abort", abortMultipartHandler(svc))
	}

	return &Server{engine: r}
}

func (s *Server) Handler() http.Handler { return s.engine }
