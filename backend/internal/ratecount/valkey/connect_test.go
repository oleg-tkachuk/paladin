package valkey

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// unreachableAddr is a port nothing listens on.
func unreachableAddr(t *testing.T) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

const testTimeout = 100 * time.Millisecond

func TestConnectToNothingAnswersNotConnectedAndLogs(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	c, stop := Connect(Options{Addr: unreachableAddr(t), Timeout: testTimeout}, zap.New(core))
	t.Cleanup(stop)

	deadline := time.Now().Add(5 * time.Second)
	for logs.Len() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if logs.Len() == 0 {
		t.Fatal("a failed dial was not logged")
	}
	for _, call := range []func() error{
		func() error { _, err := c.Bump(context.Background(), "k"); return err },
		func() error { _, err := c.BumpIfFits(context.Background(), "k", 1); return err },
		func() error { _, err := c.Read(context.Background(), "k"); return err },
	} {
		if err := call(); !errors.Is(err, ErrNotConnected) {
			t.Fatalf("call before connecting: %v, want ErrNotConnected", err)
		}
	}
	stop()
	stop() // a second stop is a no-op
}

func TestDialNeedsAnAddress(t *testing.T) {
	if _, err := Dial(Options{}); err == nil {
		t.Fatal("dialed without an address")
	}
}
