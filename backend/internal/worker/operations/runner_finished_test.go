package operations

import (
	"errors"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
)

// A result the repository refused because the operation was already cancelled
// or reclaimed is the expected end of that race, not a failed write: it is
// reported at Info. Any other refusal still warns.
func TestLogTerminalWrite(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantLevel zapcore.Level
	}{
		{"finished elsewhere", operationh.ErrOperationFinished, zapcore.InfoLevel},
		{"wrapped finished elsewhere", errors.Join(errors.New("op 1"), operationh.ErrOperationFinished), zapcore.InfoLevel},
		{"a real failure", errors.New("connection refused"), zapcore.WarnLevel},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			r := &Runner{Logger: zap.New(core)}
			r.logTerminalWrite(zap.New(core), operationh.StateSucceeded, tc.err)
			entries := logs.All()
			if len(entries) != 1 {
				t.Fatalf("logged %d entries, want 1", len(entries))
			}
			if entries[0].Level != tc.wantLevel {
				t.Errorf("level = %s, want %s (%q)", entries[0].Level, tc.wantLevel, entries[0].Message)
			}
		})
	}
}
