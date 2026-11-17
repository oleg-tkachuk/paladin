package capabilityh

import (
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/limes"
)

// Every error a capability operation answers is logged by what it is: a
// refusal the caller can act on at warn, a failure of the store or signer at
// error. The handler used to log neither.
func TestAnswerLogsByOutcome(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		code  connect.Code
		level zapcore.Level
	}{
		{"not found", fmt.Errorf("capability: %w", limes.ErrNotFound), connect.CodeNotFound, zapcore.WarnLevel},
		{"store failure", errors.New("connection reset"), connect.CodeInternal, zapcore.ErrorLevel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			h := &Handler{log: zap.New(core)}
			err := h.answer("revoke", tc.err, recordExceptions)
			if connect.CodeOf(err) != tc.code {
				t.Fatalf("code = %v, want %v", connect.CodeOf(err), tc.code)
			}
			lines := logs.FilterLevelExact(tc.level).All()
			if len(lines) != 1 || lines[0].ContextMap()["op"] != "revoke" || lines[0].ContextMap()["error"] == nil {
				t.Fatalf("want one %v line naming the op and cause, got %v", tc.level, logs.All())
			}
		})
	}
	if (&Handler{log: zap.NewNop()}).answer("revoke", nil, nil) != nil {
		t.Fatal("no error must answer no error")
	}
}
