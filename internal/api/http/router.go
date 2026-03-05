package httpapi

import (
	"net/http"
	"sync/atomic"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/generated/api"
	"github.com/oleg-tkachuk/paladin/internal/middleware"
	"github.com/oleg-tkachuk/paladin/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

type Server struct {
	engine *gin.Engine
}

func NewServer(cfg *config.Config, log *zap.Logger, svc domain.ObjectsService, catSvc domain.CategoryService, tenantSvc domain.TenantService, auditRepo domain.AuditLogRepository, metadata domain.AppMetadata, hs *service.HealthService, sysSvc domain.SystemService, started *atomic.Bool) *Server {
	gin.SetMode(cfg.Server.Mode)
	r := gin.New()

	if len(cfg.Server.HTTP.TrustedProxies) > 0 {
		if err := r.SetTrustedProxies(cfg.Server.HTTP.TrustedProxies); err != nil {
			log.Warn("Failed to set trusted proxies", zap.Error(err))
		}
	} else {
		_ = r.SetTrustedProxies(nil)
	}

	// Add API version header to ALL responses (including /version, /health/*, etc.)
	r.Use(middleware.APIVersionMiddleware(metadata.Version))

	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

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
	middleware.SetupHTTPStack(r, cfg, log, auditRepo)

	// Apply rate limiting middleware
	r.Use(middleware.RateLimitMiddleware(cfg))

	r.GET("/version", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"version":    metadata.Version,
			"commit":     metadata.Commit,
			"build_time": metadata.BuildTime,
		})
	})

	v1 := r.Group("/v1")

	// Add rate limiting headers (placeholder for future implementation)
	v1.Use(middleware.RateLimitHeadersMiddleware())

	// Register generated handlers
	adapter := NewOpenAPIAdapter(cfg, svc, catSvc, auditRepo, hs, sysSvc, started, metadata)
	api.RegisterHandlers(v1, adapter)

	// Admin-only routes: tenant lifecycle management.
	// Gated by AdminOnly middleware (requires Authorization: Bearer <admin_key>).
	admin := v1.Group("/admin")
	admin.Use(middleware.AdminOnly(cfg))
	{
		tenantHandler := NewTenantHandler(tenantSvc)
		admin.GET("/tenants", tenantHandler.ListTenants)
		admin.POST("/tenants", tenantHandler.CreateTenant)
		admin.GET("/tenants/:tenant_id", tenantHandler.GetTenant)
		admin.DELETE("/tenants/:tenant_id", tenantHandler.DeleteTenant)
		admin.PATCH("/tenants/:tenant_id/metadata", tenantHandler.PatchTenantMetadata)
	}

	return &Server{engine: r}
}

func (s *Server) Handler() http.Handler { return s.engine }
