package errors_test

import (
	"context"
	goerrors "errors"
	"net/http"
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/errors"
	apperrors "github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/utils"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAppError_ErrorAndUnwrap(t *testing.T) {
	baseErr := goerrors.New("base error")
	appErr := errors.New(errors.CodeBadRequest, "bad input", baseErr)

	assert.Equal(t, "[bad_request] bad input: base error", appErr.Error())
	assert.Equal(t, baseErr, appErr.Unwrap())

	appErrNoBase := errors.New(errors.CodeInternal, "something went wrong", nil)
	assert.Equal(t, "[internal] something went wrong", appErrNoBase.Error())
	assert.NoError(t, appErrNoBase.Unwrap())
}

func TestAppError_WithContext(t *testing.T) {
	err := errors.Internal("test", nil).WithContext("reqID", "123").WithContext("user", "bob")
	assert.Equal(t, "123", err.Details["reqID"])
	assert.Equal(t, "bob", err.Details["user"])
}

func TestAppError_WithStack(t *testing.T) {
	err := errors.Internal("test", nil).WithStack()
	assert.NotEmpty(t, err.StackTrace)
}

func TestAppError_WithFieldError(t *testing.T) {
	err := errors.ValidationFailed("invalid data", nil).
		WithFieldError("email", "invalid_format", "not a valid email").
		WithFieldError("age", "too_young", "must be > 18")

	assert.Len(t, err.FieldErrors, 2)
	assert.Equal(t, "email", err.FieldErrors[0].Field)
	assert.Equal(t, "invalid_format", err.FieldErrors[0].Code)
	assert.Equal(t, "not a valid email", err.FieldErrors[0].Message)
}

func TestConstructors(t *testing.T) {
	tests := []struct {
		err          *errors.AppError
		expectedCode string
	}{
		{errors.BadRequest("msg", nil), errors.CodeBadRequest},
		{errors.ValidationFailed("msg", nil), errors.CodeValidationFailed},
		{errors.Unauthorized("msg", nil), errors.CodeUnauthorized},
		{errors.Forbidden("msg", nil), errors.CodeForbidden},
		{errors.NotFound("msg", nil), errors.CodeNotFound},
		{errors.Conflict("msg", nil), errors.CodeConflict},
		{errors.PreconditionFailed("msg", nil), errors.CodePreconditionFailed},
		{errors.TooLarge("msg", nil), errors.CodeTooLarge},
		{errors.RateLimited("msg", nil), errors.CodeRateLimited},
		{errors.Timeout("msg", nil), errors.CodeTimeout},
		{errors.ServiceUnavailable("msg", nil), errors.CodeDependencyUnavailable},
		{errors.Internal("msg", nil), errors.CodeInternal},
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
		appErr := errors.NotFound("user not found", nil).
			WithContext("user_id", 1).
			WithFieldError("id", "missing", "required")

		code, bodyResp := errors.MapToHTTP(ctx, appErr)

		assert.Equal(t, http.StatusNotFound, code)

		// MapToHTTP now returns a ProblemDetail (RFC 7807)
		pd, ok := bodyResp.(errors.ProblemDetail)
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
		code, bodyResp := errors.MapToHTTP(ctx, goerrors.New("standard error"))
		assert.Equal(t, http.StatusInternalServerError, code)

		pd, ok := bodyResp.(errors.ProblemDetail)
		assert.True(t, ok)
		assert.Equal(t, errors.CodeInternal, "internal") // stable type code
		assert.Equal(t, http.StatusInternalServerError, pd.Status)
		assert.Equal(t, "Internal Server Error", pd.Title)
	})

	// Test all basic status mappings
	mappings := map[*errors.AppError]int{
		errors.BadRequest("m", nil):         http.StatusBadRequest,
		errors.ValidationFailed("m", nil):   http.StatusBadRequest,
		errors.Unauthorized("m", nil):       http.StatusUnauthorized,
		errors.Forbidden("m", nil):          http.StatusForbidden,
		errors.Conflict("m", nil):           http.StatusConflict,
		errors.PreconditionFailed("m", nil): http.StatusPreconditionFailed,
		errors.TooLarge("m", nil):           http.StatusRequestEntityTooLarge,
		errors.RateLimited("m", nil):        http.StatusTooManyRequests,
		errors.Timeout("m", nil):            http.StatusGatewayTimeout,
		errors.ServiceUnavailable("m", nil): http.StatusServiceUnavailable,
		errors.Internal("m", nil):           http.StatusInternalServerError,
		errors.New("unknown", "m", nil):     http.StatusInternalServerError,
	}

	for err, expCode := range mappings {
		c, _ := errors.MapToHTTP(context.Background(), err)
		assert.Equal(t, expCode, c, "expected %d for %s", expCode, err.Code)
	}
}

func TestMapToGRPC(t *testing.T) {
	t.Run("AppError", func(t *testing.T) {
		err := errors.MapToGRPC(errors.NotFound("not found", nil))
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
		assert.Equal(t, "not found", st.Message())
	})

	t.Run("UnknownError", func(t *testing.T) {
		err := errors.MapToGRPC(goerrors.New("raw error"))
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})

	// Check mappings
	mappings := map[*errors.AppError]codes.Code{
		errors.BadRequest("m", nil):         codes.InvalidArgument,
		errors.ValidationFailed("m", nil):   codes.InvalidArgument,
		errors.Unauthorized("m", nil):       codes.Unauthenticated,
		errors.Forbidden("m", nil):          codes.PermissionDenied,
		errors.Conflict("m", nil):           codes.Aborted,
		errors.PreconditionFailed("m", nil): codes.FailedPrecondition,
		errors.TooLarge("m", nil):           codes.ResourceExhausted,
		errors.RateLimited("m", nil):        codes.ResourceExhausted,
		errors.Timeout("m", nil):            codes.DeadlineExceeded,
		errors.ServiceUnavailable("m", nil): codes.Unavailable,
		errors.Internal("m", nil):           codes.Internal,
		errors.New("unknown", "m", nil):     codes.Internal,
	}

	for err, expCode := range mappings {
		grpcErr := errors.MapToGRPC(err)
		st, _ := status.FromError(grpcErr)
		assert.Equal(t, expCode, st.Code(), "expected %v for %s", expCode, err.Code)
	}
}

func TestIsNotFound(t *testing.T) {
	assert.True(t, errors.IsNotFound(errors.NotFound("x", nil)))
	assert.False(t, errors.IsNotFound(errors.Internal("x", nil)))
	assert.False(t, errors.IsNotFound(goerrors.New("raw error")))
}

func TestMapToHTTPProblem_RFC7807Fields(t *testing.T) {
	ctx := context.Background()

	t.Run("NotFoundProblem", func(t *testing.T) {
		err := errors.NotFound("object not found", nil)
		statusCode, pd := errors.MapToHTTPProblem(ctx, err, "/v1/objects/abc")
		assert.Equal(t, 404, statusCode)
		assert.Equal(t, 404, pd.Status)
		assert.Equal(t, "Not Found", pd.Title)
		assert.Equal(t, apperrors.ProblemTypeBase+"not_found", pd.Type)
		assert.Equal(t, "object not found", pd.Detail)
		assert.Equal(t, "/v1/objects/abc", pd.Instance)
	})

	t.Run("ValidationFailedWithFieldErrors", func(t *testing.T) {
		err := errors.ValidationFailed("request invalid", nil).
			WithFieldError("content_type", "required", "content_type is required").
			WithFieldError("size_bytes", "min", "size_bytes must be > 0")
		statusCode, pd := errors.MapToHTTPProblem(ctx, err, "/v1/objects")
		assert.Equal(t, 400, statusCode)
		assert.Equal(t, "Validation Failed", pd.Title)
		assert.Len(t, pd.Errors, 2)
		assert.Equal(t, "content_type", pd.Errors[0].Field)
		assert.Equal(t, "size_bytes", pd.Errors[1].Field)
	})

	t.Run("WithExtensions", func(t *testing.T) {
		err := errors.Conflict("object already exists", nil).
			WithContext("tenant_id", "acme").
			WithContext("object_id", "123")
		statusCode, pd := errors.MapToHTTPProblem(ctx, err, "/v1/objects")
		assert.Equal(t, 409, statusCode)
		assert.NotNil(t, pd.Extensions)
		assert.Equal(t, "acme", pd.Extensions["tenant_id"])
	})

	t.Run("UnknownErrorBecomesInternal", func(t *testing.T) {
		statusCode, pd := errors.MapToHTTPProblem(ctx, goerrors.New("some unexpected error"), "/v1/objects")
		assert.Equal(t, 500, statusCode)
		assert.Equal(t, "Internal Server Error", pd.Title)
		assert.Equal(t, apperrors.ProblemTypeBase+"internal", pd.Type)
	})

	t.Run("AllStatusCodeMappings", func(t *testing.T) {
		cases := []struct {
			err      *errors.AppError
			expected int
		}{
			{errors.BadRequest("m", nil), 400},
			{errors.ValidationFailed("m", nil), 400},
			{errors.Unauthorized("m", nil), 401},
			{errors.Forbidden("m", nil), 403},
			{errors.NotFound("m", nil), 404},
			{errors.Conflict("m", nil), 409},
			{errors.PreconditionFailed("m", nil), 412},
			{errors.TooLarge("m", nil), 413},
			{errors.RateLimited("m", nil), 429},
			{errors.Timeout("m", nil), 504},
			{errors.ServiceUnavailable("m", nil), 503},
			{errors.Internal("m", nil), 500},
		}
		for _, tc := range cases {
			code, pd := errors.MapToHTTPProblem(ctx, tc.err, "/")
			assert.Equal(t, tc.expected, code, "wrong status for %s", tc.err.Code)
			assert.Equal(t, tc.expected, pd.Status, "wrong pd.Status for %s", tc.err.Code)
			assert.NotEmpty(t, pd.Type)
			assert.NotEmpty(t, pd.Title)
		}
	})
}
