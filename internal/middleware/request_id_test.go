package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Generate New ID", func(t *testing.T) {
		r := gin.New()
		r.Use(RequestID(zap.NewNop(), false))
		r.GET("/test", func(c *gin.Context) {
			rid := c.Request.Context().Value(utils.RequestIDKey).(string)
			assert.NotEmpty(t, rid)
			c.Status(http.StatusOK)
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.NotEmpty(t, w.Header().Get(HeaderRequestID))
	})

	t.Run("Trust Incoming ID", func(t *testing.T) {
		r := gin.New()
		r.Use(RequestID(zap.NewNop(), false))
		r.GET("/test", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set(HeaderRequestID, "existing-id")
		r.ServeHTTP(w, req)

		assert.Equal(t, "existing-id", w.Header().Get(HeaderRequestID))
	})

	t.Run("Trust Tenant ID", func(t *testing.T) {
		r := gin.New()
		r.Use(RequestID(zap.NewNop(), true))
		r.GET("/test", func(c *gin.Context) {
			tenant := c.Request.Context().Value(utils.TenantIDKey).(string)
			assert.Equal(t, "test-tenant", tenant)
			c.Status(http.StatusOK)
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set(HeaderTenantID, "test-tenant")
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}
