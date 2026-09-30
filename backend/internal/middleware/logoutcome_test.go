package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

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
			connect.NewError(c.code, errors.New("boom")))
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
		1500*time.Millisecond, connect.NewError(connect.CodeInternal, errors.New("pool exhausted")))

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
		connect.NewError(connect.CodeInternal, errors.New("boom")))
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
	reject := connect.UnaryInterceptorFunc(func(connect.UnaryFunc) connect.UnaryFunc {
		return func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
			return nil, connect.NewError(connect.CodeUnauthenticated,
				errors.New("missing Authorization header"))
		}
	})

	mux := http.NewServeMux()
	path, handler := paladiniamv1connect.NewUserSettingsServiceHandler(&stubSettings{},
		// The production order: outcome logging first, auth second.
		connect.WithInterceptors(LogOutcome(zap.New(core)), reject),
	)
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := paladiniamv1connect.NewUserSettingsServiceClient(srv.Client(), srv.URL)
	if _, err := client.GetMine(context.Background(),
		connect.NewRequest(&iamv1.GetMineRequest{})); err == nil {
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
