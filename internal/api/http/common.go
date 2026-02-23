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

// problemContentType is the MIME type required by RFC 7807.
const problemContentType = "application/problem+json"

// respondWithError writes an RFC 7807 Problem Details response.
// The hint parameter is the caller's suggested HTTP status code; it is only
// used when the error cannot be mapped from an AppError (e.g. raw bind errors).
// Pass 0 to let the error code fully determine the status.
func respondWithError(c *gin.Context, hint int, err error) {
	// Ensure we always have an AppError so the status is derived from its code.
	var appErr *errors.AppError
	if !stdErrors.As(err, &appErr) {
		switch hint {
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
			err = errors.Internal(err.Error(), nil)
		}
	}

	instance := c.Request.RequestURI
	if instance == "" {
		instance = c.Request.URL.Path
	}

	ctx := c.Request.Context()
	status, pd := errors.MapToHTTPProblem(ctx, err, instance)

	// Log based on severity.
	log := logger.FromContext(ctx)
	fields := []zap.Field{
		zap.Int("status", status),
		zap.String("path", c.Request.URL.Path),
		zap.String("request_id", utils.RequestIDFromContext(ctx, "")),
	}
	if status >= 500 {
		log.Error("request failed", append(fields, zap.Error(err))...)
	} else if status >= 400 {
		log.Warn("request rejected", fields...)
	}

	c.Header("Content-Type", problemContentType)
	c.JSON(status, pd)
}

func tenantID(c *gin.Context) string {
	// Middleware guarantees TenantID is present.
	return utils.TenantIDFromContext(c.Request.Context(), "")
}
