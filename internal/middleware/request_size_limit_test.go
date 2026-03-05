package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRequestSizeLimitMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Within Limit", func(t *testing.T) {
		r := gin.New()
		r.Use(RequestSizeLimitMiddleware(10))
		r.POST("/test", func(c *gin.Context) {
			body, _ := io.ReadAll(c.Request.Body)
			assert.Equal(t, "hello", string(body))
			c.Status(http.StatusOK)
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader("hello"))
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("Exceeds Limit", func(t *testing.T) {
		r := gin.New()
		r.Use(RequestSizeLimitMiddleware(5))
		r.POST("/test", func(c *gin.Context) {
			_, err := io.ReadAll(c.Request.Body)
			if err != nil {
				c.Error(http.ErrHandlerTimeout) // Simulate MaxBytesReader error handling in middleware

				return
			}
			c.Status(http.StatusOK)
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader("too long body"))
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})
}
