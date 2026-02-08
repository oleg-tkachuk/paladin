package httpapi

import (
	stdErrors "errors"
	"net/http"

	"github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func respondWithError(c *gin.Context, code int, err error) {
	// If the error is not an AppError, attempt to wrap it based on the suggested status code.
	// This ensures generic errors (like binding errors) are mapped correctly to HTTP responses.
	var appErr *errors.AppError
	if !stdErrors.As(err, &appErr) {
		switch code {
		case http.StatusBadRequest:
			err = errors.BadRequest(err.Error(), nil)
		case http.StatusNotFound:
			err = errors.NotFound(err.Error(), nil)
		case http.StatusUnauthorized:
			err = errors.Unauthorized(err.Error(), nil)
		case http.StatusForbidden:
			err = errors.Forbidden(err.Error(), nil)
		case http.StatusConflict:
			err = errors.Conflict(err.Error(), nil)
		case http.StatusPreconditionFailed:
			err = errors.PreconditionFailed(err.Error(), nil)
		case http.StatusRequestEntityTooLarge:
			err = errors.TooLarge(err.Error(), nil)
		case http.StatusTooManyRequests:
			err = errors.RateLimited(err.Error(), nil)
		default:
			// If code is 5xx or unknown, fallback to Internal
			err = errors.Internal(err.Error(), nil)
		}
	}

	status, body := errors.MapToHTTP(c.Request.Context(), err)

	// Log based on severity
	log := logger.FromContext(c.Request.Context())
	if status >= 500 {
		log.Error("Request failed", zap.Error(err), zap.Int("status", status), zap.String("path", c.Request.URL.Path))
	} else if status >= 400 {
		// Warn for client errors
		log.Warn("Request failed", zap.Error(err), zap.Int("status", status), zap.String("path", c.Request.URL.Path))
	}

	c.JSON(status, body)
}

func tenantID(c *gin.Context) string {
	// Middleware guarantees TenantID is present.
	return utils.TenantIDFromContext(c.Request.Context(), "")
}
