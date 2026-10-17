package logger

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const testAddr = ":8085"

// The server's own errors reach the structured log at warn, naming the
// listener they arrived on.
func TestHTTPServerErrorLog(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	HTTPServerErrorLog(zap.New(core), testAddr).Print("http: TLS handshake error")
	all := logs.All()
	if len(all) != 1 {
		t.Fatalf("lines = %d, want 1", len(all))
	}
	if all[0].Level != zapcore.WarnLevel || all[0].ContextMap()["listen_addr"] != testAddr {
		t.Errorf("line = %+v", all[0])
	}
}

// Every request's context carries the server's logger.
func TestHTTPBaseContext(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	ctx := HTTPBaseContext(zap.New(core))(nil)
	FromContext(ctx).Info("served")
	if logs.Len() != 1 {
		t.Errorf("the base context's logger is not the server's")
	}
}
