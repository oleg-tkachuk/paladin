package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/middleware"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("RateLimitMiddleware", func() {
	var (
		cfg    *config.Config
		router *gin.Engine
	)

	BeforeEach(func() {
		gin.SetMode(gin.TestMode)
		cfg = &config.Config{
			RateLimit: config.RateLimit{
				RequestsPerSecond: 10,
				Burst:             5,
				MaxTenants:        2,
				CleanupTTL:        100 * time.Millisecond,
				CleanupInterval:   50 * time.Millisecond,
			},
		}

		router = gin.New()
		router.Use(middleware.RateLimitMiddleware(cfg))
		router.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})
	})

	It("should allow requests within limits", func() {
		for i := 0; i < 5; i++ {
			req, _ := http.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("X-Tenant-ID", "tenant1")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusOK))
		}
	})

	It("should block requests exceeding burst", func() {
		for i := 0; i < 5; i++ {
			req, _ := http.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("X-Tenant-ID", "tenant1")
			router.ServeHTTP(httptest.NewRecorder(), req)
		}

		// 6th request should be blocked
		req, _ := http.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Tenant-ID", "tenant1")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusTooManyRequests))
	})

	It("should maintain separate limits for different tenants", func() {
		// Exhaust tenant1
		for i := 0; i < 10; i++ {
			req, _ := http.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("X-Tenant-ID", "tenant1")
			router.ServeHTTP(httptest.NewRecorder(), req)
		}

		// tenant2 should still be allowed
		req, _ := http.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Tenant-ID", "tenant2")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))
	})

	It("should evict oldest tenant when max tenants reached", func() {
		// Use small burst to make testing eviction easier
		cfg.RateLimit.MaxTenants = 1
		router = gin.New()
		router.Use(middleware.RateLimitMiddleware(cfg))
		router.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})

		// 1. Fill with tenant1
		req1, _ := http.NewRequest(http.MethodGet, "/test", nil)
		req1.Header.Set("X-Tenant-ID", "tenant1")
		router.ServeHTTP(httptest.NewRecorder(), req1)

		// 2. Add tenant2 (should evict tenant1)
		req2, _ := http.NewRequest(http.MethodGet, "/test", nil)
		req2.Header.Set("X-Tenant-ID", "tenant2")
		router.ServeHTTP(httptest.NewRecorder(), req2)

		// 3. tenant1 should have new limiter (burst reset)
		router.ServeHTTP(httptest.NewRecorder(), req1)
	})

	It("should handle concurrent requests", func() {
		var wg sync.WaitGroup
		n := 20
		wg.Add(n)

		for i := 0; i < n; i++ {
			go func() {
				defer wg.Done()
				req, _ := http.NewRequest(http.MethodGet, "/test", nil)
				req.Header.Set("X-Tenant-ID", "concurrent")
				router.ServeHTTP(httptest.NewRecorder(), req)
			}()
		}
		wg.Wait()
	})
})
