package errors

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"

	"paladin/internal/utils"

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
	Code       string
	Message    string
	Err        error
	Details    map[string]any
	StackTrace string
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

// ErrorResponse is the unified error structure
type ErrorResponse struct {
	Error ErrorDetails `json:"error"`
}

// ErrorDetails contains the error information
type ErrorDetails struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Details   string `json:"details,omitempty"`
	RequestID string `json:"request_id"`
	TraceID   string `json:"trace_id"`
}

// MapToHTTP maps an error to an HTTP status code and response body
func MapToHTTP(ctx context.Context, err error) (int, ErrorResponse) {
	var appErr *AppError
	if !errors.As(err, &appErr) {
		// Default to internal error if unknown
		// Log the actual error in the caller, here we just return the generic response
		return http.StatusInternalServerError, ErrorResponse{
			Error: ErrorDetails{
				Code:      CodeInternal,
				Message:   "Internal server error",
				RequestID: utils.RequestIDFromContext(ctx, ""),
				TraceID:   utils.TraceIDFromContext(ctx, ""),
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

	// If Details map is present, we might want to convert it to string or pick a specific message.
	// For now, consistent with requirements, we keep 'details' as optional string.
	// We'll use appErr.Message for 'details' if it differs from the standard message?
	// Or just put appErr.Message in 'message'.
	// Requirement: message = short human readable. details = optional context.

	// Let's use appErr.Message as the main message.
	// If appErr.Err (wrapped error) is present, maybe use that as details?
	// But requirements say "no stack traces". appErr.Err.Error() might be safe or might be technical.
	// Let's stick to simple mapping for now. `appErr.Message` -> `message`.

	details := ""
	if appErr.Err != nil {
		// Be careful not to expose sensitive info, but typically appErr.Err is the cause.
		// For internal errors, we might mask this. For 4xx, it might be useful.
		// Requirement 4: details is OPTIONAL and MUST contain additional context only (no stack traces).
		details = appErr.Err.Error()
	}

	return statusCode, ErrorResponse{
		Error: ErrorDetails{
			Code:      appErr.Code,
			Message:   appErr.Message,
			Details:   details,
			RequestID: reqID,
			TraceID:   traceID,
		},
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
