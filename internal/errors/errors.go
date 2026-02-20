package errors

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/oleg-tkachuk/paladin/internal/utils"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Canonical Error Codes
const (
	CodeBadRequest            = "bad_request"
	CodeValidationFailed      = "validation_failed"
	CodeUnauthorized          = "unauthorized"
	CodeForbidden             = "forbidden"
	CodeNotFound              = "not_found"
	CodeConflict              = "conflict"
	CodePreconditionFailed    = "precondition_failed"
	CodeTooLarge              = "too_large"
	CodeRateLimited           = "rate_limited"
	CodeTimeout               = "timeout"
	CodeDependencyUnavailable = "dependency_unavailable"
	CodeInternal              = "internal"
)

// AppError is the standard error type for the application
type AppError struct {
	Code        string
	Message     string
	Err         error
	Details     map[string]any
	FieldErrors []FieldError
	StackTrace  string
}

// FieldError represents a validation error for a specific field
type FieldError struct {
	Field   string
	Code    string
	Message string
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error {
	return e.Err
}

// New creates a new AppError
func New(code, message string, err error) *AppError {
	return &AppError{
		Code:    code,
		Message: message,
		Err:     err,
		Details: make(map[string]any),
	}
}

// WithContext adds contextual information to the error
func (e *AppError) WithContext(key string, value interface{}) *AppError {
	if e.Details == nil {
		e.Details = make(map[string]any)
	}
	e.Details[key] = value
	return e
}

// WithStack captures the current stack trace
func (e *AppError) WithStack() *AppError {
	if e.StackTrace == "" {
		e.StackTrace = string(debug.Stack())
	}
	return e
}

// WithFieldError adds a field-level validation error
func (e *AppError) WithFieldError(field, code, message string) *AppError {
	if e.FieldErrors == nil {
		e.FieldErrors = []FieldError{}
	}
	e.FieldErrors = append(e.FieldErrors, FieldError{
		Field:   field,
		Code:    code,
		Message: message,
	})
	return e
}

// Helper constructors
func BadRequest(msg string, err error) *AppError       { return New(CodeBadRequest, msg, err) }
func ValidationFailed(msg string, err error) *AppError { return New(CodeValidationFailed, msg, err) }
func Unauthorized(msg string, err error) *AppError     { return New(CodeUnauthorized, msg, err) }
func Forbidden(msg string, err error) *AppError        { return New(CodeForbidden, msg, err) }
func NotFound(msg string, err error) *AppError         { return New(CodeNotFound, msg, err) }
func Conflict(msg string, err error) *AppError         { return New(CodeConflict, msg, err) }
func PreconditionFailed(msg string, err error) *AppError {
	return New(CodePreconditionFailed, msg, err)
}
func TooLarge(msg string, err error) *AppError    { return New(CodeTooLarge, msg, err) }
func RateLimited(msg string, err error) *AppError { return New(CodeRateLimited, msg, err) }
func Timeout(msg string, err error) *AppError     { return New(CodeTimeout, msg, err) }
func ServiceUnavailable(msg string, err error) *AppError {
	return New(CodeDependencyUnavailable, msg, err)
}
func Internal(msg string, err error) *AppError { return New(CodeInternal, msg, err) }

// MapToHTTP maps an error to an HTTP status code and response body
func MapToHTTP(ctx context.Context, err error) (int, any) {
	var appErr *AppError
	if !errors.As(err, &appErr) {
		// Default to internal error if unknown
		reqID := utils.RequestIDFromContext(ctx, "")
		traceID := utils.TraceIDFromContext(ctx, "")

		return http.StatusInternalServerError, map[string]any{
			"error": map[string]any{
				"code":       CodeInternal,
				"message":    "Internal server error",
				"request_id": reqID,
				"trace_id":   traceID,
			},
		}
	}

	statusCode := http.StatusInternalServerError
	switch appErr.Code {
	case CodeBadRequest, CodeValidationFailed:
		statusCode = http.StatusBadRequest
	case CodeUnauthorized:
		statusCode = http.StatusUnauthorized
	case CodeForbidden:
		statusCode = http.StatusForbidden
	case CodeNotFound:
		statusCode = http.StatusNotFound
	case CodeConflict:
		statusCode = http.StatusConflict
	case CodePreconditionFailed:
		statusCode = http.StatusPreconditionFailed
	case CodeTooLarge:
		statusCode = http.StatusRequestEntityTooLarge
	case CodeRateLimited:
		statusCode = http.StatusTooManyRequests
	case CodeTimeout:
		statusCode = http.StatusGatewayTimeout
	case CodeDependencyUnavailable:
		statusCode = http.StatusServiceUnavailable
	case CodeInternal:
		statusCode = http.StatusInternalServerError
	}

	traceID := utils.TraceIDFromContext(ctx, "")
	reqID := utils.RequestIDFromContext(ctx, "")

	// Build error response
	errorResp := map[string]any{
		"code":       appErr.Code,
		"message":    appErr.Message,
		"request_id": reqID,
	}

	// Add trace_id if present
	if traceID != "" {
		errorResp["trace_id"] = traceID
	}

	// Add details if present
	if len(appErr.Details) > 0 {
		errorResp["details"] = appErr.Details
	}

	// Add field_errors if present
	if len(appErr.FieldErrors) > 0 {
		fieldErrors := make([]map[string]string, len(appErr.FieldErrors))
		for i, fe := range appErr.FieldErrors {
			fieldErrors[i] = map[string]string{
				"field":   fe.Field,
				"code":    fe.Code,
				"message": fe.Message,
			}
		}
		errorResp["field_errors"] = fieldErrors
	}

	return statusCode, map[string]any{
		"error": errorResp,
	}
}

// MapToGRPC maps an error to a gRPC status error
func MapToGRPC(err error) error {
	var appErr *AppError
	if !errors.As(err, &appErr) {
		return status.Error(codes.Internal, "Internal server error")
	}

	var code codes.Code
	switch appErr.Code {
	case CodeBadRequest, CodeValidationFailed:
		code = codes.InvalidArgument
	case CodeUnauthorized:
		code = codes.Unauthenticated
	case CodeForbidden:
		code = codes.PermissionDenied
	case CodeNotFound:
		code = codes.NotFound
	case CodeConflict:
		code = codes.Aborted // Or AlreadyExists, but Aborted is better for concurrency issues
	case CodePreconditionFailed:
		code = codes.FailedPrecondition
	case CodeTooLarge, CodeRateLimited:
		code = codes.ResourceExhausted
	case CodeTimeout:
		code = codes.DeadlineExceeded
	case CodeDependencyUnavailable:
		code = codes.Unavailable
	case CodeInternal:
		code = codes.Internal
	default:
		code = codes.Internal
	}

	return status.Error(code, appErr.Message)
}

// IsNotFound checks if the error is a NotFound error
func IsNotFound(err error) bool {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Code == CodeNotFound
	}
	return false
}
