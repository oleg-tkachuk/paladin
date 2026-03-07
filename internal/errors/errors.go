package errors

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/aws/smithy-go"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/utils"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// problemTypeBase is the base URI for RFC 7807 problem types.
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

// AppError is the standard error type for the application.
type AppError struct {
	Code        string
	Message     string
	Err         error
	Details     map[string]any
	FieldErrors []FieldError
	StackTrace  string
}

// FieldError represents a validation error for a specific field.
type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ProblemDetail is the RFC 7807 Problem Details response body.
// Content-Type must be "application/problem+json".
type ProblemDetail struct {
	// Type is a URI reference that identifies the problem type. Stable per error code.
	Type string `json:"type"`
	// Title is a short, human-readable summary of the problem type.
	Title string `json:"title"`
	// Status is the HTTP status code.
	Status int `json:"status"`
	// Detail is a human-readable explanation specific to this occurrence.
	Detail string `json:"detail"`
	// Instance is a URI reference that identifies this specific occurrence (e.g. the request path).
	Instance string `json:"instance,omitempty"`
	// RequestID correlates this error to a specific request.
	RequestID string `json:"request_id,omitempty"`
	// TraceID links to distributed trace if available.
	TraceID string `json:"trace_id,omitempty"`
	// Errors holds per-field validation errors (extension beyond RFC 7807).
	Errors []FieldError `json:"errors,omitempty"`
	// Extensions holds any extra context key/value pairs.
	Extensions map[string]any `json:"extensions,omitempty"`
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

// New creates a new AppError.
func New(code, message string, err error) *AppError {
	return &AppError{
		Code:    code,
		Message: message,
		Err:     err,
		Details: make(map[string]any),
	}
}

// WithContext adds contextual information to the error.
func (e *AppError) WithContext(key string, value interface{}) *AppError {
	if e.Details == nil {
		e.Details = make(map[string]any)
	}
	e.Details[key] = value

	return e
}

// WithStack captures the current stack trace (for internal errors only).
func (e *AppError) WithStack() *AppError {
	if e.StackTrace == "" {
		e.StackTrace = string(debug.Stack())
	}

	return e
}

// WithFieldError adds a field-level validation error.
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

// statusCodeForAppError maps an AppError code to its HTTP status code.
func statusCodeForAppError(code string) int {
	switch code {
	case CodeBadRequest, CodeValidationFailed:
		return http.StatusBadRequest
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflict:
		return http.StatusConflict
	case CodePreconditionFailed:
		return http.StatusPreconditionFailed
	case CodeTooLarge:
		return http.StatusRequestEntityTooLarge
	case CodeRateLimited:
		return http.StatusTooManyRequests
	case CodeTimeout:
		return http.StatusGatewayTimeout
	case CodeDependencyUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// titleForCode returns a short, stable, human-readable title for a given error code.
func titleForCode(code string) string {
	switch code {
	case CodeBadRequest:
		return "Bad Request"
	case CodeValidationFailed:
		return "Validation Failed"
	case CodeUnauthorized:
		return "Unauthorized"
	case CodeForbidden:
		return "Forbidden"
	case CodeNotFound:
		return "Not Found"
	case CodeConflict:
		return "Conflict"
	case CodePreconditionFailed:
		return "Precondition Failed"
	case CodeTooLarge:
		return "Payload Too Large"
	case CodeRateLimited:
		return "Too Many Requests"
	case CodeTimeout:
		return "Gateway Timeout"
	case CodeDependencyUnavailable:
		return "Service Unavailable"
	default:
		return "Internal Server Error"
	}
}

// MapToHTTPProblem maps an error to an RFC 7807 ProblemDetail and HTTP status code.
// instance should be the request URI (e.g. c.Request.RequestURI).
func MapToHTTPProblem(ctx context.Context, err error, instance string) (int, ProblemDetail) {
	reqID := utils.RequestIDFromContext(ctx, "")
	traceID := utils.TraceIDFromContext(ctx, "")

	var appErr *AppError
	if !errors.As(err, &appErr) {
		if errors.Is(err, domain.ErrNotFound) {
			appErr = NotFound("resource not found", err)
		} else {
			// Wrap unknown errors as internal.
			appErr = Internal("an unexpected error occurred", err)
		}
	}

	httpStatus := statusCodeForAppError(appErr.Code)

	pd := ProblemDetail{
		Type:      ProblemTypeBase + appErr.Code,
		Title:     titleForCode(appErr.Code),
		Status:    httpStatus,
		Detail:    appErr.Message,
		Instance:  instance,
		RequestID: reqID,
		TraceID:   traceID,
	}

	if len(appErr.FieldErrors) > 0 {
		pd.Errors = appErr.FieldErrors
	}

	if len(appErr.Details) > 0 {
		pd.Extensions = appErr.Details
	}

	return httpStatus, pd
}

// MapToHTTP maps an error to an HTTP status code and response body (legacy; prefer MapToHTTPProblem).
// Kept for backward compatibility with non-HTTP layers.
func MapToHTTP(ctx context.Context, err error) (int, any) {
	status, pd := MapToHTTPProblem(ctx, err, "")

	return status, pd
}

// MapToGRPC maps an error to a gRPC status error.
func MapToGRPC(err error) error {
	var appErr *AppError
	if !errors.As(err, &appErr) {
		if errors.Is(err, domain.ErrNotFound) {
			return status.Error(codes.NotFound, "not found")
		}

		return status.Error(codes.Internal, "internal server error")
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
		code = codes.Aborted
	case CodePreconditionFailed:
		code = codes.FailedPrecondition
	case CodeTooLarge, CodeRateLimited:
		code = codes.ResourceExhausted
	case CodeTimeout:
		code = codes.DeadlineExceeded
	case CodeDependencyUnavailable:
		code = codes.Unavailable
	default:
		code = codes.Internal
	}

	return status.Error(code, appErr.Message)
}

// IsNotFound checks if the error is a NotFound error.
func IsNotFound(err error) bool {
	if errors.Is(err, domain.ErrNotFound) {
		return true
	}

	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Code == CodeNotFound
	}

	return false
}

// IsS3NotFound checks if the error is an S3 NotFound error.
func IsS3NotFound(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		return code == "NotFound" || code == "NoSuchKey" || code == "NoSuchBucket"
	}
	return false
}
