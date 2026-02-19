package middleware

import (
	"net/http"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/domain"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// SetupHTTPStack configures the canonical middleware stack for Gin
func SetupHTTPStack(r *gin.Engine, cfg *config.Config, log *zap.Logger, auditRepo domain.AuditLogRepository) {

	// 1. Recovery (Panic -> 500)
	r.Use(gin.Recovery())

	// 2. Security Headers (protect all responses)
	r.Use(SecurityHeadersMiddleware())

	// 3. Request Size Limit (prevent resource exhaustion)
	maxRequestSize := int64(10 * 1024 * 1024) // 10MB default
	r.Use(RequestSizeLimitMiddleware(maxRequestSize))

	// 4. CORS (Optional, good for web)
	allowedOrigins := cfg.Server.HTTP.CORSAllowedOrigins
	if len(allowedOrigins) == 0 {
		allowedOrigins = []string{"*"}
	}
	r.Use(cors.New(cors.Config{
		AllowOrigins:     allowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Request-ID", "X-Tenant-ID", "Idempotency-Key"},
		ExposeHeaders:    []string{"Content-Length", "X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// 5. RequestID (and weak tenant extraction if trusted)
	r.Use(RequestID(log, cfg.Security.TrustTenantIDFromRequest))

	// 6. ContextLogger (attaches log-with-rid to ctx)
	r.Use(ContextLogger(log))

	// 7. Logger (Structured Zap)
	r.Use(RequestLogger(log, cfg.Server.LogProbes))

	// 8. Audit Logging (captured after logging enrichment)
	r.Use(AuditLogMiddleware(auditRepo, log))

	// 8. Legacy: Removed, relies on trusted headers (X-Tenant-ID) established by Linkerd mTLS mesh.
	// r.Use(Auth(authorizer, cfg.Security, log))

	// 9. Enforce Tenant & Limits
	r.Use(EnforceTenant(cfg.Security))

	// 10. OpenTelemetry Tracing & Metrics
	if cfg.OTel.Enabled {
		r.Use(func(c *gin.Context) {
			OTelHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c.Request = r
				c.Next()
			})).ServeHTTP(c.Writer, c.Request)
		})
	}
}
