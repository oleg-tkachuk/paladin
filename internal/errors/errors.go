package errors

import (
	"errors"
	"fmt"
	"net/http"

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
	Code    string
	Message string
	Err     error
	Details map[string]any
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
	}
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
func Internal(msg string, err error) *AppError    { return New(CodeInternal, msg, err) }

// MapToHTTP maps an error to an HTTP status code and response body
func MapToHTTP(err error) (int, map[string]any) {
	var appErr *AppError
	if !errors.As(err, &appErr) {
		// Default to internal error if unknown
		return http.StatusInternalServerError, map[string]any{
			"error":   CodeInternal,
			"details": "Internal server error",
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

	resp := map[string]any{
		"error":   appErr.Code,
		"details": appErr.Message,
	}
	if len(appErr.Details) > 0 {
		resp["metadata"] = appErr.Details
	}

	return statusCode, resp
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
