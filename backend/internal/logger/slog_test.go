package logger

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// A line written through the slog logger keeps the name, under the parent's.
func TestSlogKeepsTheName(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	Slog(zap.New(core).Named("server"), "capability-issuer").Info("minted")
	if got := logs.All(); len(got) != 1 || got[0].LoggerName != "server.capability-issuer" {
		t.Fatalf("logged %+v, want one line named server.capability-issuer", got)
	}
}
