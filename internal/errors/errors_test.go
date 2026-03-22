package errors_test

import (
	"context"
	goerrors "errors"
	"net/http"
	"testing"

	apperrors "github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppError_ErrorAndUnwrap(t *testing.T) {
	baseErr := goerrors.New("base error")
	appErr := apperrors.New(apperrors.CodeBadRequest, "bad input", baseErr)

	assert.Equal(t, "[bad_request] bad input: base error", appErr.Error())
	assert.Equal(t, baseErr, appErr.Unwrap())

	appErrNoBase := apperrors.New(apperrors.CodeInternal, "something went wrong", nil)
	assert.Equal(t, "[internal] something went wrong", appErrNoBase.Error())
	require.NoError(t, appErrNoBase.Unwrap())
}

func TestAppError_WithContext(t *testing.T) {
	err := apperrors.Internal("test", nil).WithContext("reqID", "123").WithContext("user", "bob")
	assert.Equal(t, "123", err.Details["reqID"])
	assert.Equal(t, "bob", err.Details["user"])
}

func TestAppError_WithStack(t *testing.T) {
	err := apperrors.Internal("test", nil).WithStack()
	assert.NotEmpty(t, err.StackTrace)
}

func TestAppError_WithFieldError(t *testing.T) {
	err := apperrors.ValidationFailed("invalid data", nil).
		WithFieldError("email", "invalid_format", "not a valid email").
		WithFieldError("age", "too_young", "must be > 18")

	assert.Len(t, err.FieldErrors, 2)
	assert.Equal(t, "email", err.FieldErrors[0].Field)
	assert.Equal(t, "invalid_format", err.FieldErrors[0].Code)
	assert.Equal(t, "not a valid email", err.FieldErrors[0].Message)
}

func TestConstructors(t *testing.T) {
	tests := []struct {
		err          *apperrors.AppError
		expectedCode string
	}{
		{apperrors.BadRequest("msg", nil), apperrors.CodeBadRequest},
		{apperrors.ValidationFailed("msg", nil), apperrors.CodeValidationFailed},
		{apperrors.Unauthorized("msg", nil), apperrors.CodeUnauthorized},
		{apperrors.Forbidden("msg", nil), apperrors.CodeForbidden},
		{apperrors.NotFound("msg", nil), apperrors.CodeNotFound},
		{apperrors.Conflict("msg", nil), apperrors.CodeConflict},
		{apperrors.PreconditionFailed("msg", nil), apperrors.CodePreconditionFailed},
		{apperrors.TooLarge("msg", nil), apperrors.CodeTooLarge},
		{apperrors.RateLimited("msg", nil), apperrors.CodeRateLimited},
		{apperrors.Timeout("msg", nil), apperrors.CodeTimeout},
		{apperrors.ServiceUnavailable("msg", nil), apperrors.CodeDependencyUnavailable},
		{apperrors.Internal("msg", nil), apperrors.CodeInternal},
	}

	for _, tc := range tests {
		assert.Equal(t, tc.expectedCode, tc.err.Code)
		assert.Equal(t, "msg", tc.err.Message)
	}
}

func TestMapToHTTP(t *testing.T) {
	ctx := context.WithValue(context.Background(), utils.RequestIDKey, "req-123")
	ctx = context.WithValue(ctx, utils.TraceIDKey, "trace-456")

	t.Run("AppError", func(t *testing.T) {
		appErr := apperrors.NotFound("user not found", nil).
			WithContext("user_id", 1).
			WithFieldError("id", "missing", "required")

		code, bodyResp := apperrors.MapToHTTP(ctx, appErr)

		assert.Equal(t, http.StatusNotFound, code)

		// MapToHTTP now returns a ProblemDetail (RFC 7807)
		pd, ok := bodyResp.(apperrors.ProblemDetail)
		assert.True(t, ok, "response should be a ProblemDetail")
		assert.Equal(t, http.StatusNotFound, pd.Status)
		assert.Equal(t, "Not Found", pd.Title)
		assert.Equal(t, "user not found", pd.Detail)
		assert.Equal(t, "req-123", pd.RequestID)
		assert.Equal(t, "trace-456", pd.TraceID)
		assert.NotNil(t, pd.Extensions)
		assert.Equal(t, 1, pd.Extensions["user_id"])
		assert.Len(t, pd.Errors, 1)
		assert.Equal(t, "id", pd.Errors[0].Field)
	})

	t.Run("UnknownError", func(t *testing.T) {
		code, bodyResp := apperrors.MapToHTTP(ctx, goerrors.New("standard error"))
		assert.Equal(t, http.StatusInternalServerError, code)

		pd, ok := bodyResp.(apperrors.ProblemDetail)
		assert.True(t, ok)
		assert.Equal(t, apperrors.CodeInternal, "internal") // stable type code
		assert.Equal(t, http.StatusInternalServerError, pd.Status)
		assert.Equal(t, "Internal Server Error", pd.Title)
	})

	// Test all basic status mappings
	mappings := map[*apperrors.AppError]int{
		apperrors.BadRequest("m", nil):         http.StatusBadRequest,
		apperrors.ValidationFailed("m", nil):   http.StatusBadRequest,
		apperrors.Unauthorized("m", nil):       http.StatusUnauthorized,
		apperrors.Forbidden("m", nil):          http.StatusForbidden,
		apperrors.Conflict("m", nil):           http.StatusConflict,
		apperrors.PreconditionFailed("m", nil): http.StatusPreconditionFailed,
		apperrors.TooLarge("m", nil):           http.StatusRequestEntityTooLarge,
		apperrors.RateLimited("m", nil):        http.StatusTooManyRequests,
		apperrors.Timeout("m", nil):            http.StatusGatewayTimeout,
		apperrors.ServiceUnavailable("m", nil): http.StatusServiceUnavailable,
		apperrors.Internal("m", nil):           http.StatusInternalServerError,
		apperrors.New("unknown", "m", nil):     http.StatusInternalServerError,
	}

	for err, expCode := range mappings {
		c, _ := apperrors.MapToHTTP(context.Background(), err)
		assert.Equal(t, expCode, c, "expected %d for %s", expCode, err.Code)
	}
}

func TestIsNotFound(t *testing.T) {
	assert.True(t, apperrors.IsNotFound(apperrors.NotFound("x", nil)))
	assert.False(t, apperrors.IsNotFound(apperrors.Internal("x", nil)))
	assert.False(t, apperrors.IsNotFound(goerrors.New("raw error")))
}

func TestMapToHTTPProblem_RFC7807Fields(t *testing.T) {
	ctx := context.Background()

	t.Run("NotFoundProblem", func(t *testing.T) {
		err := apperrors.NotFound("object not found", nil)
		statusCode, pd := apperrors.MapToHTTPProblem(ctx, err, "/v1/objects/abc")
		assert.Equal(t, 404, statusCode)
		assert.Equal(t, 404, pd.Status)
		assert.Equal(t, "Not Found", pd.Title)
		assert.Equal(t, apperrors.ProblemTypeBase+"not_found", pd.Type)
		assert.Equal(t, "object not found", pd.Detail)
		assert.Equal(t, "/v1/objects/abc", pd.Instance)
	})

	t.Run("ValidationFailedWithFieldErrors", func(t *testing.T) {
		err := apperrors.ValidationFailed("request invalid", nil).
			WithFieldError("content_type", "required", "content_type is required").
			WithFieldError("size_bytes", "min", "size_bytes must be > 0")
		statusCode, pd := apperrors.MapToHTTPProblem(ctx, err, "/v1/objects")
		assert.Equal(t, 400, statusCode)
		assert.Equal(t, "Validation Failed", pd.Title)
		assert.Len(t, pd.Errors, 2)
		assert.Equal(t, "content_type", pd.Errors[0].Field)
		assert.Equal(t, "size_bytes", pd.Errors[1].Field)
	})

	t.Run("WithExtensions", func(t *testing.T) {
		err := apperrors.Conflict("object already exists", nil).
			WithContext("tenant_id", "acme").
			WithContext("object_id", "123")
		statusCode, pd := apperrors.MapToHTTPProblem(ctx, err, "/v1/objects")
		assert.Equal(t, 409, statusCode)
		assert.NotNil(t, pd.Extensions)
		assert.Equal(t, "acme", pd.Extensions["tenant_id"])
	})

	t.Run("UnknownErrorBecomesInternal", func(t *testing.T) {
		statusCode, pd := apperrors.MapToHTTPProblem(ctx, goerrors.New("some unexpected error"), "/v1/objects")
		assert.Equal(t, 500, statusCode)
		assert.Equal(t, "Internal Server Error", pd.Title)
		assert.Equal(t, apperrors.ProblemTypeBase+"internal", pd.Type)
	})

	t.Run("AllStatusCodeMappings", func(t *testing.T) {
		cases := []struct {
			err      *apperrors.AppError
			expected int
		}{
			{apperrors.BadRequest("m", nil), 400},
			{apperrors.ValidationFailed("m", nil), 400},
			{apperrors.Unauthorized("m", nil), 401},
			{apperrors.Forbidden("m", nil), 403},
			{apperrors.NotFound("m", nil), 404},
			{apperrors.Conflict("m", nil), 409},
			{apperrors.PreconditionFailed("m", nil), 412},
			{apperrors.TooLarge("m", nil), 413},
			{apperrors.RateLimited("m", nil), 429},
			{apperrors.Timeout("m", nil), 504},
			{apperrors.ServiceUnavailable("m", nil), 503},
			{apperrors.Internal("m", nil), 500},
		}
		for _, tc := range cases {
			code, pd := apperrors.MapToHTTPProblem(ctx, tc.err, "/")
			assert.Equal(t, tc.expected, code, "wrong status for %s", tc.err.Code)
			assert.Equal(t, tc.expected, pd.Status, "wrong pd.Status for %s", tc.err.Code)
			assert.NotEmpty(t, pd.Type)
			assert.NotEmpty(t, pd.Title)
		}
	})
}
