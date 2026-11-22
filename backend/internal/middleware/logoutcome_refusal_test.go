package middleware

import (
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
)

// errDependencyDown is a dependency failing under a call: the server's fault.
var errDependencyDown = errors.New("dial tcp 10.0.0.7:5432: connection refused")

// loggedOutcome is the one line logFailure writes for err.
func loggedOutcome(t *testing.T, err error) observer.LoggedEntry {
	t.Helper()
	core, logs := observer.New(zap.DebugLevel)
	logFailure(zap.New(core), "/paladin.data.v1.ObjectService/PutObject", "req-1", time.Millisecond, err)
	all := logs.All()
	if len(all) != 1 {
		t.Fatalf("wrote %d lines, want 1", len(all))
	}
	return all[0]
}

// The bucket gate decides on the bucket's state and says no: each of its
// refusals, as the handlers answer it, is logged as a refusal at Warn — the
// provisioning one too, whose code is Unavailable.
func TestABucketGateRefusalIsLoggedAsARefusal(t *testing.T) {
	for _, r := range bucketgate.Refusals() {
		t.Run(r.Error(), func(t *testing.T) {
			got := loggedOutcome(t, objecth.MapResolveErr(r))
			if got.Message != msgRPCRefused || got.Level != zapcore.WarnLevel {
				t.Errorf("logged %q at %v, want %q at %v", got.Message, got.Level, msgRPCRefused, zapcore.WarnLevel)
			}
		})
	}
}

// An Unavailable from a dependency failing is still the server's fault,
// logged at Error; so is one without a cause, which nothing marks.
func TestADependencyFailureStaysAServerFault(t *testing.T) {
	for name, err := range map[string]error{
		"with its cause":   rpcerr.New(connect.CodeUnavailable, errDependencyDown),
		"without a cause":  connect.NewError(connect.CodeUnavailable, "retry the call"),
		"internal, caused": rpcerr.New(connect.CodeInternal, errDependencyDown),
	} {
		t.Run(name, func(t *testing.T) {
			got := loggedOutcome(t, err)
			if got.Message != msgRPCFailed || got.Level != zapcore.ErrorLevel {
				t.Errorf("logged %q at %v, want %q at %v", got.Message, got.Level, msgRPCFailed, zapcore.ErrorLevel)
			}
		})
	}
}
