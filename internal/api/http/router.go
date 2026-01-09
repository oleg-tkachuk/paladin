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
)

type Server struct {
	engine *gin.Engine
}

func NewServer(cfg *config.Config, log *zap.Logger, svc service.ObjectsService, version, commit, buildTime string, hs *service.HealthService, started *atomic.Bool) *Server {
	gin.SetMode(cfg.Server.Mode)
	r := gin.New()

	// Use canonical stack
	middleware.SetupHTTPStack(r, cfg, log)

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

	v1 := r.Group("/v1")
	{
		v1.POST("/objects", createObjectHandler(log, svc))
		v1.GET("/objects/:id", getObjectHandler(log, svc))
		v1.GET("/objects/:id/meta", getObjectMetaHandler(log, svc))
		v1.POST("/objects/:id/complete", completeObjectHandler(log, svc))
		v1.DELETE("/objects/:id", deleteObjectHandler(log, svc))

		v1.POST("/multipart", initiateMultipartHandler(log, svc))
		v1.POST("/multipart/:upload_id/parts/:part_number/sign", signPartHandler(log, svc))
		v1.POST("/multipart/:upload_id/complete", completeMultipartHandler(log, svc))
		v1.POST("/multipart/:upload_id/abort", abortMultipartHandler(log, svc))
	}

	return &Server{engine: r}
}

func (s *Server) Handler() http.Handler { return s.engine }
