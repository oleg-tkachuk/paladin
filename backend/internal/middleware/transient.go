package middleware

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"
)

// unavailableMessage is all a caller is told when the server's database
// cannot be reached: the driver's text names hosts and users.
const unavailableMessage = "the service's database is unavailable; retry the call"

// ClassifyTransient gives a failure the server reported as its own fault —
// Internal, Unknown, DataLoss — the code of what actually happened, when that
// is not the server's fault:
//
//   - the caller cancelled, or its deadline passed: Canceled or
//     DeadlineExceeded, which a caller reads as its own doing;
//   - the database could not be reached or is going away (unavailable
//     reports it): Unavailable, which a caller reads as worth a retry.
//
// The original error stays the cause, for the failure log. A handler that
// chose a precise code keeps it. unavailable may be nil, and recognises
// nothing then; it is injected so this package needs no database driver.
//
// Install it inside LogOutcome, so the failure is logged at the level of its
// real code, and inside ScrubInternal.
func ClassifyTransient(unavailable func(error) bool) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			err := next(ctx, spec, stream)
			return reclassify(ctx.Err(), err, unavailable)
		}
	}
}

// reclassify is the whole decision: err with the code of what happened, given
// ctxErr, the call's context error.
func reclassify(ctxErr, err error, unavailable func(error) bool) error {
	if err == nil {
		return nil
	}
	if cerr := new(connect.Error); errors.As(err, &cerr) && !scrubbedCodes[cerr.Code()] {
		return err
	}
	switch {
	case errors.Is(ctxErr, context.Canceled):
		return connect.NewError(connect.CodeCanceled, ctxErr.Error()).WithCause(err)
	case errors.Is(ctxErr, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, ctxErr.Error()).WithCause(err)
	case unavailable != nil && unavailable(err):
		return connect.NewError(connect.CodeUnavailable, unavailableMessage).WithCause(err)
	}
	return err
}
