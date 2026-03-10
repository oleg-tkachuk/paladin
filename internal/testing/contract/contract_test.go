package contract

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/gin-gonic/gin"
	"github.com/oleg-tkachuk/paladin/internal/middleware"
	"github.com/oleg-tkachuk/paladin/internal/openapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContract(t *testing.T) {
	// 1. Load the existing OpenAPI 3.x YAML spec
	spec, err := openapi.LoadSpec()
	require.NoError(t, err, "failed to load spec")

	// Adjust spec servers for local testing if needed, or handle prefix.
	// Since we are running in-memory, we can choose how to route.
	// If the spec has /v1 as server, we should probably strip it for routing match if the router expects exact path match from 'paths'.
	// However, let's see how gorillamux router behaves. It typically matches paths as defined in 'paths' section.
	// So if 'paths' has '/objects', it expects '/objects'.
	// To match '/v1/objects', we ideally should strip /v1 before passing to router.
	// OR we can update the spec paths to include /v1 for this test context, but that's hacking.
	// Best practice: Middleware wraps the router. If the app is mounted at /v1, the router inside sees /objects.
	// But here the middleware is global.

	// Let's assume the request comes in as /v1/objects.
	// We can add a simple prefix stripper in the middleware or test helper if needed.
	// Use a helper middleware to strip /v1 equivalent to http.StripPrefix if we were using stdlib.

	gin.SetMode(gin.TestMode)
	router := gin.New()

	// Add the OAPI validation middleware
	// Note: We might need to handle /v1 prefix.
	// For this test, let's try to request paths that match the spec 'paths' directly to check validation logic,
	// ignoring the 'servers' configuration for routing purposes, as that is often handled by infrastructure (ingress/gateway).
	// So we will request `/objects` not `/v1/objects` in this test to verify the logic against the schema.
	// If the real app uses /v1, it likely strips it before routing or defines paths with /v1.
	// But existing 'openapi.yaml' has paths starting with /.

	router.Use(func(c *gin.Context) {
		// Hack to stripping /v1 for the validation middleware if we decide to use /v1 in request
		// But simpler: just test /objects directly.
		c.Next()
	})

	router.Use(middleware.OAPIValidationMiddleware(spec))

	// Match spec route /v1/health/livez
	router.GET("/v1/health/livez", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "alive"})
	})

	router.GET("/v1/objects", func(c *gin.Context) {
		// Mock response for ListObjects
		c.JSON(http.StatusOK, gin.H{
			"items": []interface{}{},
			"pagination": gin.H{
				"hasMore":    false,
				"nextCursor": nil,
				"totalCount": int64(0),
			},
		})
	})

	router.POST("/v1/objects", func(c *gin.Context) {
		// Mock handler for POST
		c.JSON(http.StatusCreated, gin.H{
			"objectId":  "550e8400-e29b-41d4-a716-446655440000",
			"objectKey": "default/550e8400-e29b-41d4-a716-446655440000",
			"bucket":    "objects",
			"status":    "pending",
			"upload": gin.H{
				"url":       "http://example.com/upload",
				"method":    "PUT",
				"expiresAt": "2026-02-17T11:14:50Z",
			},
		})
	})

	// 4. Response validation helper
	validateResponse := func(t *testing.T, req *http.Request, resp *http.Response) {
		ctx := req.Context()

		oapiRouter, err := gorillamux.NewRouter(spec)
		require.NoError(t, err)

		route, pathParams, err := oapiRouter.FindRoute(req)
		require.NoError(t, err, "failed to find route in spec")

		err = openapi3filter.ValidateResponse(ctx, &openapi3filter.ResponseValidationInput{
			RequestValidationInput: &openapi3filter.RequestValidationInput{
				Request:    req,
				PathParams: pathParams,
				Route:      route,
			},
			Status: resp.StatusCode,
			Header: resp.Header,
			Body:   resp.Body,
			Options: &openapi3filter.Options{
				ExcludeRequestBody: true,
				AuthenticationFunc: func(c context.Context, input *openapi3filter.AuthenticationInput) error {
					return nil
				},
			},
		})
		assert.NoError(t, err, "response validation failed")
	}

	t.Run("GET /v1/health/livez", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/health/livez", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		validateResponse(t, req, w.Result())
	})

	t.Run("GET /v1/objects - Valid", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/objects", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		validateResponse(t, req, w.Result())
	})

	t.Run("GET /v1/objects - Query Validation (Middleware)", func(t *testing.T) {
		// 'status' query param must be one of enum if provided
		req := httptest.NewRequest(http.MethodGet, "/v1/objects?status=INVALID", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		// Middleware should validate query params and return 400
		assert.Equal(t, http.StatusBadRequest, w.Code)

		var body map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &body)
		assert.Contains(t, body, "error")
	})

	t.Run("POST /v1/objects - Body Validation (Middleware)", func(t *testing.T) {
		// Missing required fields
		req := httptest.NewRequest(http.MethodPost, "/v1/objects", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}
