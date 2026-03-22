package paladinapi

import (
	"errors"

	"connectrpc.com/connect"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	apperrors "github.com/oleg-tkachuk/paladin/internal/errors"
)

// mapError converts a domain error (AppError) to a Connect error with
// the correct status code.
func mapError(err error) error {
	var appErr *apperrors.AppError
	if !errors.As(err, &appErr) {
		if errors.Is(err, domain.ErrNotFound) {
			return connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}

		return connect.NewError(connect.CodeInternal, errors.New("internal server error"))
	}

	var code connect.Code
	switch appErr.Code {
	case apperrors.CodeBadRequest, apperrors.CodeValidationFailed:
		code = connect.CodeInvalidArgument
	case apperrors.CodeUnauthorized:
		code = connect.CodeUnauthenticated
	case apperrors.CodeForbidden:
		code = connect.CodePermissionDenied
	case apperrors.CodeNotFound:
		code = connect.CodeNotFound
	case apperrors.CodeConflict:
		code = connect.CodeAlreadyExists
	case apperrors.CodePreconditionFailed:
		code = connect.CodeFailedPrecondition
	case apperrors.CodeTooLarge, apperrors.CodeRateLimited:
		code = connect.CodeResourceExhausted
	case apperrors.CodeTimeout:
		code = connect.CodeDeadlineExceeded
	case apperrors.CodeDependencyUnavailable:
		code = connect.CodeUnavailable
	default:
		code = connect.CodeInternal
	}

	return connect.NewError(code, errors.New(appErr.Message))
}
