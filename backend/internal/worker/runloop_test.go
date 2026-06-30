package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunTicker_InvokesFnPerTickThenExitsOnCancel(t *testing.T) {
	t.Parallel()
	var ticks int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunTicker(ctx, "test", 5*time.Millisecond, func(context.Context) error {
			if atomic.AddInt32(&ticks, 1) >= 3 {
				cancel() // stop after a few ticks
			}
			return nil
		})
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunTicker did not return after cancel")
	}
	if got := atomic.LoadInt32(&ticks); got < 3 {
		t.Fatalf("want >=3 ticks, got %d", got)
	}
}

func TestRunTicker_NonPositiveIntervalIsNoOp(t *testing.T) {
	t.Parallel()
	called := false
	err := RunTicker(context.Background(), "disabled", 0, func(context.Context) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("want nil for interval<=0, got %v", err)
	}
	if called {
		t.Fatal("fn must not run when interval<=0")
	}
}

// A fn error must NOT stop the loop — it only drives the metric outcome.
func TestRunTicker_FnErrorDoesNotStopLoop(t *testing.T) {
	t.Parallel()
	var ticks int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = RunTicker(ctx, "errs", 5*time.Millisecond, func(context.Context) error {
			if atomic.AddInt32(&ticks, 1) >= 3 {
				cancel()
			}
			return errors.New("tick failed")
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunTicker did not return after cancel")
	}
	if got := atomic.LoadInt32(&ticks); got < 3 {
		t.Fatalf("loop stopped early on fn error: got %d ticks", got)
	}
}
