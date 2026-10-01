package app

import (
	"encoding/json"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// http.Server writes its own errors — TLS handshake failures, malformed
// requests — through ErrorLog, which defaults to Go's global logger: raw
// stderr lines with no structure and no listener attached. On a pod serving
// two listeners that produces messages nobody can attribute, which is exactly
// how "client sent an HTTP request to an HTTPS server from 127.0.0.1" stayed
// unattributed.
func TestBuildHTTPServer_ErrorLogIsStructuredAndTagged(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	srv, err := BuildHTTPServer(config.HTTPServer{Addr: "0.0.0.0:8085"}, nil, zap.New(core))
	if err != nil {
		t.Fatal(err)
	}

	if srv.ErrorLog == nil {
		t.Fatal("ErrorLog is nil — the server's own errors go to Go's global logger")
	}
	srv.ErrorLog.Printf("http: TLS handshake error from 127.0.0.1:1234: %s",
		"client sent an HTTP request to an HTTPS server")

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("captured %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Level != zapcore.WarnLevel {
		t.Errorf("level = %v, want warn", e.Level)
	}
	if got := e.ContextMap()["listen_addr"]; got != "0.0.0.0:8085" {
		t.Errorf("listen_addr = %v — without it the message cannot be attributed "+
			"to a listener on a pod that serves two", got)
	}
	if e.LoggerName != "http" {
		t.Errorf("logger name = %q, want http", e.LoggerName)
	}
}

// The whole point is that it is machine-readable now; assert the shape rather
// than trusting the encoder.
func TestBuildHTTPServer_ErrorLogEncodesAsJSON(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	srv, err := BuildHTTPServer(config.HTTPServer{Addr: "0.0.0.0:8080"}, nil, zap.New(core))
	if err != nil {
		t.Fatal(err)
	}
	srv.ErrorLog.Print("boom")

	if len(logs.All()) != 1 {
		t.Fatal("nothing captured")
	}
	b, err := json.Marshal(logs.All()[0].ContextMap())
	if err != nil {
		t.Fatalf("context is not serialisable: %v", err)
	}
	if string(b) == "{}" {
		t.Error("no fields attached — the message is as anonymous as before")
	}
}
