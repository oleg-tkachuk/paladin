package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
)

// errPanicked is what a caller sees for a handler that panicked: the internal
// error, with nothing of the panic in it.
var errPanicked = errors.New("internal error")

// Recover answers a panic in anything inside it as an internal error and logs
// it with its stack, instead of the panic taking the connection down. Install
// it outermost, so a panic in an interceptor is caught as well as one in a
// handler.
func Recover(l *zap.Logger) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) (err error) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}
				// http.ErrAbortHandler is net/http's own way to abort a
				// response; it is not a defect, and re-panicking keeps its
				// meaning.
				if recovered == http.ErrAbortHandler { //nolint:errorlint // a panic value, compared as net/http does
					panic(recovered)
				}
				l.Error("rpc handler panicked",
					zap.String("procedure", spec.Procedure),
					zap.String("panic", fmt.Sprint(recovered)),
					zap.StackSkip("stack", 1),
				)
				err = connect.NewError(connect.CodeInternal, errPanicked.Error()).WithCause(errPanicked)
			}()
			return next(ctx, spec, stream)
		}
	}
}
