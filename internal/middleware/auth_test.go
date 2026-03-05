package middleware_test

import (
	"net/http"
	"net/http/httptest"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/middleware"
	"github.com/oleg-tkachuk/paladin/internal/utils"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("AuthMiddleware", func() {
	var (
		cfg    *config.Config
		router *gin.Engine
	)

	BeforeEach(func() {
		gin.SetMode(gin.TestMode)
		cfg = &config.Config{
			Auth: config.Auth{
				Enabled: true,
			},
			Security: config.Security{
				RejectTenantMismatch: true,
			},
		}

		router = gin.New()
		// Inject tenant into context (simulating RequestID/Auth middleware)
		router.Use(func(c *gin.Context) {
			tenantID := c.GetHeader("X-Tenant-ID")
			if tenantID != "" {
				ctx := utils.WithTenantID(c.Request.Context(), tenantID)
				c.Request = c.Request.WithContext(ctx)
			}
			c.Next()
		})
		router.Use(middleware.EnforceTenant(cfg))
		router.GET("/v1/:tenant_id/test", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})
	})

	It("should allow request with matching tenant", func() {
		req, _ := http.NewRequest(http.MethodGet, "/v1/tenant1/test", nil)
		req.Header.Set("X-Tenant-ID", "tenant1")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusOK))
	})

	It("should block request with missing tenant context", func() {
		req, _ := http.NewRequest(http.MethodGet, "/v1/tenant1/test", nil)
		// No X-Tenant-ID header
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
	})

	It("should block request with tenant mismatch when configured", func() {
		req, _ := http.NewRequest(http.MethodGet, "/v1/tenant1/test", nil)
		req.Header.Set("X-Tenant-ID", "tenant2") // Mismatch
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusForbidden))
	})

	It("should allow request with tenant mismatch when not configured to reject", func() {
		cfg.Security.RejectTenantMismatch = false
		router = gin.New()
		router.Use(func(c *gin.Context) {
			ctx := utils.WithTenantID(c.Request.Context(), "tenant2")
			c.Request = c.Request.WithContext(ctx)
			c.Next()
		})
		router.Use(middleware.EnforceTenant(cfg))
		router.GET("/v1/:tenant_id/test", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})

		req, _ := http.NewRequest(http.MethodGet, "/v1/tenant1/test", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusOK))
	})
})
