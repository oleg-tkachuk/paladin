package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
)

// panicValue is what the handler below panics with: text that must reach the
// log and never the caller.
const panicValue = "nil map write in handler: tenant 7f3c secret path /var/lib/paladin"

// panicLogMessage is the line Recover writes for a panic.
const panicLogMessage = "rpc handler panicked"

// A panicking handler is answered Internal with nothing of the panic in it,
// and the panic is logged with the procedure and a stack — the caller learns
// the server failed, the operator learns where.
func TestRecoverAnswersAPanicAsInternal(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	probe := &unarytest.Probe{OnCall: func(context.Context) error {
		panic(panicValue)
	}}

	_, err := unarytest.CallProbe(context.Background(), probe,
		[]connect.ServerInterceptor{Recover(zap.New(core))})

	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("err = %v, want a connect error", err)
	}
	if cerr.Code() != connect.CodeInternal || cerr.Message() != errPanicked.Error() {
		t.Errorf("caller got %v %q, want internal %q", cerr.Code(), cerr.Message(), errPanicked.Error())
	}
	if strings.Contains(err.Error(), panicValue) {
		t.Errorf("the panic reached the caller: %v", err)
	}

	found := logs.FilterMessage(panicLogMessage).All()
	if len(found) != 1 {
		t.Fatalf("a panic wrote %d log lines, want 1", len(found))
	}
	line := found[0]
	if line.Level != zap.ErrorLevel {
		t.Errorf("logged at %v, want error", line.Level)
	}
	fields := line.ContextMap()
	if fields["procedure"] != unarytest.ProbeProcedure {
		t.Errorf("procedure = %v, want %s", fields["procedure"], unarytest.ProbeProcedure)
	}
	if fields["panic"] != panicValue {
		t.Errorf("panic = %v, want %q", fields["panic"], panicValue)
	}
	if stack, _ := fields["stack"].(string); stack == "" {
		t.Error("no stack: the line says a handler panicked and not where")
	}
}

// http.ErrAbortHandler is net/http's way to abort a response, not a defect:
// Recover lets it through as the panic it is, and does not log it.
func TestRecoverRepanicsAnAbortedHandler(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	next := func(context.Context, connect.Spec, connect.ServerStream) error {
		panic(http.ErrAbortHandler)
	}

	defer func() {
		if got := recover(); got != http.ErrAbortHandler { //nolint:errorlint // a panic value, compared as net/http does
			t.Errorf("recovered %v, want http.ErrAbortHandler re-panicked", got)
		}
		if n := logs.Len(); n != 0 {
			t.Errorf("an aborted handler wrote %d log lines, want 0", n)
		}
	}()
	_ = Recover(zap.New(core))(next)(context.Background(), connect.Spec{Procedure: unarytest.ProbeProcedure}, nil)
	t.Error("Recover swallowed http.ErrAbortHandler")
}
