package httpapi

import (
	"net/http"
	"sync/atomic"

	"paladin/internal/config"
	apperrors "paladin/internal/errors"
	"paladin/internal/generated/api"
	"paladin/internal/middleware"
	"paladin/internal/service"

	"github.com/gin-gonic/gin"
	validator "github.com/oapi-codegen/gin-middleware"
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
		r.Use(middleware.RateLimitMiddleware(10, 20))
	}

	// Load OpenAPI spec for validation
	swagger, err := api.GetSwagger()
	if err != nil {
		log.Fatal("Failed to load OpenAPI spec", zap.Error(err))
	}

	v1 := r.Group("/v1")

	// Add OpenAPI validation middleware
	validatorOptions := &validator.Options{
		ErrorHandler: func(c *gin.Context, message string, statusCode int) {
			// Map validation errors to our standard error format
			respondWithError(c, statusCode, apperrors.ValidationFailed(message, nil))
		},
	}
	v1.Use(validator.OapiRequestValidatorWithOptions(swagger, validatorOptions))

	// Register generated handlers
	adapter := NewOpenAPIAdapter(svc)
	api.RegisterHandlers(v1, adapter)

	return &Server{engine: r}
}

func (s *Server) Handler() http.Handler { return s.engine }
