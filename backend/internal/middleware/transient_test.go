package middleware

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
)

// driverText is what a database driver says of a refused connection: an
// address the caller must never see.
const driverText = "failed to connect to `user=app database=app`: 10.0.0.7:5432: connection refused"

// errUnreachable stands for an error the classifier recognises.
var errUnreachable = errors.New(driverText)

func isUnreachable(err error) bool { return errors.Is(err, errUnreachable) }

func TestReclassify(t *testing.T) {
	internal := connect.NewError(connect.CodeInternal, driverText).WithCause(errUnreachable)
	other := connect.NewError(connect.CodeInternal, "boom")
	refused := connect.NewError(connect.CodeNotFound, "no such user").WithCause(errUnreachable)
	cases := []struct {
		name     string
		ctxErr   error
		err      error
		classify func(error) bool
		want     connect.Code
		same     bool
	}{
		{name: "success", err: nil, classify: isUnreachable, want: 0, same: true},
		{name: "database unreachable", err: internal, classify: isUnreachable, want: connect.CodeUnavailable},
		{name: "unknown failure", err: other, classify: isUnreachable, want: connect.CodeInternal, same: true},
		{name: "no classifier", err: internal, classify: nil, want: connect.CodeInternal, same: true},
		{name: "a precise code wins", err: refused, classify: isUnreachable, want: connect.CodeNotFound, same: true},
		{name: "caller went away", ctxErr: context.Canceled, err: internal, classify: isUnreachable, want: connect.CodeCanceled},
		{name: "deadline passed", ctxErr: context.DeadlineExceeded, err: other, classify: isUnreachable, want: connect.CodeDeadlineExceeded},
		{name: "plain error from a cancelled call", ctxErr: context.Canceled, err: errors.New("x"), classify: nil, want: connect.CodeCanceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reclassify(tc.ctxErr, tc.err, tc.classify)
			if tc.err == nil {
				if got != nil {
					t.Fatalf("success became %v", got)
				}
				return
			}
			if code := connect.CodeOf(got); code != tc.want {
				t.Fatalf("code = %v, want %v (%v)", code, tc.want, got)
			}
			if tc.same && got != tc.err { //nolint:errorlint // identity: the error must come back untouched, not rewrapped
				t.Errorf("the error was rebuilt: %v", got)
			}
			if !tc.same && !errors.Is(got, tc.err) {
				t.Errorf("the original error is not the cause of %v", got)
			}
		})
	}
}

// Through the chain the planes install: the caller is told the database is
// unavailable, in a message that names none of it, while the failure log
// inside sees the reclassified code with the original as its cause.
func TestClassifyTransientThroughAHandler(t *testing.T) {
	var logged error
	logSeat := connect.ServerInterceptor(func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			logged = next(ctx, spec, stream)
			return logged
		}
	})
	probe := &unarytest.Probe{OnCall: func(context.Context) error {
		return connect.NewError(connect.CodeInternal, driverText).WithCause(errUnreachable)
	}}
	interceptors := []connect.ServerInterceptor{unarytest.LeavesRequest(t), ScrubInternal(), logSeat, ClassifyTransient(isUnreachable)}

	_, err := unarytest.CallProbe(context.Background(), probe, interceptors)
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("caller got %v, want unavailable", err)
	}
	if strings.Contains(err.Error(), "10.0.0.7") {
		t.Errorf("the caller saw the driver's text: %v", err)
	}
	if connect.CodeOf(logged) != connect.CodeUnavailable || !errors.Is(logged, errUnreachable) {
		t.Errorf("the failure log saw %v, want unavailable caused by the driver's error", logged)
	}
}
