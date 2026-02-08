package middleware

import (
	"context"
	"fmt"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/gin-gonic/gin"
)

// OAPIValidationMiddleware creates a Gin middleware that validates requests against the given OpenAPI spec.
func OAPIValidationMiddleware(spec *openapi3.T) gin.HandlerFunc {
	// We use gorillamux router adapter because it's the standard one in kin-openapi to route requests
	// based on the OpenAPI spec paths.
	router, err := gorillamux.NewRouter(spec)
	if err != nil {
		// This should only happen if the spec is invalid or has issues that NewRouter catches.
		// Since we validate the spec in LoadSpec, this might panic or log fatal if strict.
		// For middleware factory, panicking is often acceptable at startup.
		panic(fmt.Sprintf("failed to create openapi router: %v", err))
	}

	return func(c *gin.Context) {
		// Find route
		route, pathParams, err := router.FindRoute(c.Request)
		if err != nil {
			// Route not found in spec.
			// Depending on strictness, we might want to return 404 or just skip validation.
			// Usually, if we want to enforce the spec, 404 is appropriate.
			// But if the API might have extra routes not in spec (e.g. health check), we might skip.
			// However, usually health checks are added to the spec or ignored here.
			// Let's assume we want to validate everything and return 404 if not found in spec.
			// Or better: check if it's a "route not found" error and let Gin handle 404 if we want,
			// but if the middleware is global, it might block non-spec routes.
			// If used as a group middleware, it should only apply to API routes.
			// Let's log it or return error.
			// For "production-grade": clear errors.
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("route not found in spec: %v", err)})
			c.Abort()
			return
		}

		// Validate Request
		requestValidationInput := &openapi3filter.RequestValidationInput{
			Request:    c.Request,
			PathParams: pathParams,
			Route:      route,
			Options: &openapi3filter.Options{
				AuthenticationFunc: func(c context.Context, input *openapi3filter.AuthenticationInput) error {
					return nil
				},
			},
		}

		if err := openapi3filter.ValidateRequest(c.Request.Context(), requestValidationInput); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("openapi validation failure: %v", err)})
			c.Abort()
			return
		}

		// If path params are found, we might want to inject them into Gin context if they differ,
		// but Gin has its own routing.
		// Validation passed.
		c.Next()
	}
}
