package middleware

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/reqctx"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// The level is a decision about who gets woken up, so it is asserted per code
// rather than at the two codes the interceptor plumbing makes easy to produce.
//
// The buckets: the server is broken (Error), the request was refused by a
// policy or precondition (Warn), the client made an ordinary mistake (Info).
func TestFailureLevelPerCode(t *testing.T) {
	cases := []struct {
		code connect.Code
		want zapcore.Level
		why  string
	}{
		{connect.CodeInternal, zap.ErrorLevel, "the server is broken"},
		{connect.CodeUnknown, zap.ErrorLevel, "an error nobody mapped is a defect until proven otherwise"},
		{connect.CodeDataLoss, zap.ErrorLevel, "the loudest thing a storage system can say"},
		{connect.CodeUnavailable, zap.ErrorLevel, "a dependency is down"},

		{connect.CodePermissionDenied, zap.WarnLevel, "Cedar refused it"},
		{connect.CodeResourceExhausted, zap.WarnLevel, "a quota bit"},
		{connect.CodeAborted, zap.WarnLevel, "an OCC race; a burst means a concurrent writer"},
		{connect.CodeFailedPrecondition, zap.WarnLevel, "state was not what the caller assumed"},

		{connect.CodeInvalidArgument, zap.InfoLevel, "a bad filter is normal traffic"},
		{connect.CodeNotFound, zap.InfoLevel, "asking for what is not there is normal traffic"},
		{connect.CodeUnauthenticated, zap.InfoLevel, "Warn here would cry wolf on every unauthenticated probe"},
		{connect.CodeAlreadyExists, zap.InfoLevel, "a retried create"},
	}
	for _, c := range cases {
		core, logs := observer.New(zap.DebugLevel)
		logFailure(zap.New(core), "/pkg.Svc/M", "req-1", time.Millisecond,
			connect.NewError(c.code, "boom"))
		all := logs.All()
		if len(all) != 1 {
			t.Fatalf("%v: wrote %d lines, want 1", c.code, len(all))
		}
		if all[0].Level != c.want {
			t.Errorf("%v logged at %v, want %v — %s", c.code, all[0].Level, c.want, c.why)
		}
	}
}

// A served request must write nothing. One line per success is a firehose that
// buries the failures, and otelconnect already counts them.
func TestSuccessIsSilent(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	logFailure(zap.New(core), "/pkg.Svc/M", "req-1", time.Millisecond, nil)
	if n := logs.Len(); n != 0 {
		t.Errorf("a successful RPC wrote %d log lines, want 0", n)
	}
}

// The fields an operator needs to act: which RPC, which code, how long, and
// which request — plus the message, so the line answers "why" without a repro.
func TestFailureCarriesTheFieldsThatMakeItActionable(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	logFailure(zap.New(core), "/paladin.admin.v1.TenantService/ListTenants", "req-42",
		1500*time.Millisecond, connect.NewError(connect.CodeInternal, "pool exhausted"))

	got := logs.All()[0].ContextMap()
	for k, want := range map[string]any{
		"rpc":        "/paladin.admin.v1.TenantService/ListTenants",
		"code":       "internal",
		"request_id": "req-42",
		"took_ms":    int64(1500),
	} {
		if got[k] != want {
			t.Errorf("field %s = %v, want %v", k, got[k], want)
		}
	}
	if msg, _ := got["err"].(string); msg == "" {
		t.Error("no err field — the line says an RPC failed and not why, which " +
			"is the half that saves a reproduction")
	}
}

// A nil logger must not panic. The interceptor is constructed from wiring that
// can legitimately pass nil in a test or a stripped-down binary, and a logging
// helper that takes the process down is worse than the silence it replaced.
func TestNilLoggerIsNotFatal(t *testing.T) {
	logFailure(nil, "/pkg.Svc/M", "", time.Millisecond,
		connect.NewError(connect.CodeInternal, "boom"))
}

// The ordering claim, asserted rather than commented.
//
// LogOutcome only sees what interceptors installed AFTER it produce. Connect
// applies the first-listed one outermost, so putting it after auth would leave
// every rejected authentication unlogged — which was the widest part of the
// gap it exists to close. That is a property of the wiring, not of the
// function, so a unit test over logFailure cannot reach it.
func TestOutcomeLoggerSeesAuthRejections(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)

	// An interceptor that rejects exactly as auth.NewInterceptor does, and a
	// handler that must therefore never run.
	reject := connect.ServerInterceptor(func(connect.ServerFunc) connect.ServerFunc {
		return func(context.Context, connect.Spec, connect.ServerStream) error {
			return connect.NewError(connect.CodeUnauthenticated,
				"missing Authorization header")
		}
	})

	client := paladiniamv1connect.NewUserSettingsServiceClient(unarytest.Client(func(s *connect.Server) {
		paladiniamv1connect.RegisterUserSettingsServiceHandler(s, &stubSettings{})
	},
		// The production order: outcome logging first, auth second.
		LogOutcome(zap.New(core)), reject,
	))
	if _, err := client.GetMine(context.Background(),
		&iamv1.GetMineRequest{}); err == nil {
		t.Fatal("expected the rejection to reach the caller")
	}

	all := logs.All()
	if len(all) != 1 {
		t.Fatalf("a rejected authentication wrote %d log lines, want 1 — if this "+
			"is 0, LogOutcome is installed inside auth and cannot see it", len(all))
	}
	if got := all[0].ContextMap()["code"]; got != "unauthenticated" {
		t.Errorf("code = %v, want unauthenticated", got)
	}
}

// A failed RPC's line carries its trace, so it joins the trace the tracing
// interceptor opened, and the request id once — not again from the context.
func TestFailureCarriesItsTrace(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled,
	})
	ctx := reqctx.WithRequestID(trace.ContextWithSpanContext(context.Background(), sc), "req-1")
	fail := func(context.Context, connect.Spec, connect.ServerStream) error {
		return connect.NewError(connect.CodeInternal, "pool closed")
	}

	_ = LogOutcome(zap.New(core))(fail)(ctx, connect.Spec{Procedure: "/paladin.iam.v1.UserSettingsService/GetMine"}, nil)

	all := logs.All()
	if len(all) != 1 {
		t.Fatalf("lines = %d, want 1", len(all))
	}
	if got := all[0].ContextMap()["trace_id"]; got != sc.TraceID().String() {
		t.Errorf("trace_id = %v, want %s", got, sc.TraceID())
	}
	n := 0
	for _, f := range all[0].Context {
		if f.Key == "request_id" {
			n++
		}
	}
	if n > 1 {
		t.Errorf("request_id written %d times", n)
	}
}

// A handler that withholds a failure's text from the caller — a store's
// error behind "retry" — keeps it as the error's cause, and the line carries
// it: the caller is told less, the operator is not.
func TestFailureCarriesTheCauseTheCallerWasNotTold(t *testing.T) {
	const cause = "pgstore: dial tcp: connection refused"
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"a withheld cause":        {connect.NewError(connect.CodeUnavailable, "retry").WithCause(errors.New(cause)), cause},
		"a cause already told":    {connect.NewError(connect.CodeUnavailable, cause).WithCause(errors.New(cause)), ""},
		"no cause":                {connect.NewError(connect.CodeUnavailable, "retry"), ""},
		"an error that is no RPC": {errors.New(cause), ""},
	} {
		t.Run(name, func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			logFailure(zap.New(core), "/pkg.Svc/M", "req-1", time.Millisecond, tc.err)
			if got, _ := logs.All()[0].ContextMap()[fieldCause].(string); got != tc.want {
				t.Errorf("cause = %q, want %q", got, tc.want)
			}
		})
	}
}
