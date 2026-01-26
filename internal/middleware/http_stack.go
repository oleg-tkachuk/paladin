package middleware

import (
	"time"

	"paladin/internal/config"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// SetupHTTPStack configures the canonical middleware stack for Gin
func SetupHTTPStack(r *gin.Engine, cfg *config.Config, log *zap.Logger) {
	// 1. Recovery (Panic -> 500)
	r.Use(gin.Recovery())

	// 2. CORS (Optional, good for web)
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"}, // Configure from config in prod
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Request-ID", "X-Tenant-ID"},
		ExposeHeaders:    []string{"Content-Length", "X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// 3. RequestID (and weak tenant extraction if trusted)
	r.Use(RequestID(log, cfg.Security.TrustTenantIDFromRequest))

	// 4. ContextLogger (attaches log-with-rid to ctx)
	r.Use(ContextLogger(log))

	// 5. Logger (Structured Zap)
	r.Use(RequestLogger(log, cfg.Server.LogProbes))

	// 5. Auth & Tenant Derivation
	r.Use(Auth(cfg.Security, log))

	// 6. Enforce Tenant & Limits
	r.Use(EnforceTenant(cfg.Security))

	// Add OTel, Metrics, RateLimit here as needed
}
