package logger

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

func TestReplaceGlobals(t *testing.T) {
	testLog := zaptest.NewLogger(t)
	ReplaceGlobals(testLog)

	if zap.L() != testLog {
		t.Error("Global logger was not replaced")
	}
}

func TestNewBootstrapLogger(t *testing.T) {
	l := NewBootstrapLogger()
	if l == nil {
		t.Fatal("Bootstrap logger is nil")
	}
}
