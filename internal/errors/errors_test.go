package errors_test

import (
	"context"
	goerrors "errors"
	"net/http"
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/errors"
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
	assert.Nil(t, appErrNoBase.Unwrap())
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

		body, ok := bodyResp.(map[string]any)
		assert.True(t, ok)
		errResp := body["error"].(map[string]any)

		assert.Equal(t, errors.CodeNotFound, errResp["code"])
		assert.Equal(t, "user not found", errResp["message"])
		assert.Equal(t, "req-123", errResp["request_id"])
		assert.Equal(t, "trace-456", errResp["trace_id"])

		details := errResp["details"].(map[string]any)
		assert.Equal(t, 1, details["user_id"])

		fields := errResp["field_errors"].([]map[string]string)
		assert.Len(t, fields, 1)
		assert.Equal(t, "id", fields[0]["field"])
	})

	t.Run("UnknownError", func(t *testing.T) {
		code, bodyResp := errors.MapToHTTP(ctx, goerrors.New("standard error"))
		assert.Equal(t, http.StatusInternalServerError, code)

		body, _ := bodyResp.(map[string]any)
		errResp := body["error"].(map[string]any)
		assert.Equal(t, errors.CodeInternal, errResp["code"])
		assert.Equal(t, "Internal server error", errResp["message"])
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
