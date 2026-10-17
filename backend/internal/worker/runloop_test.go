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

// keepFirst keeps the earliest failure: a later one, or a later nil, must not
// replace it, and a nil before any failure must not hide the failure after.
func TestKeepFirst(t *testing.T) {
	first, second := errors.New("first"), errors.New("second")
	for name, tc := range map[string]struct {
		in   []error
		want error
	}{
		"nothing failed":         {in: []error{nil, nil}, want: nil},
		"one failure":            {in: []error{nil, first, nil}, want: first},
		"the earliest failure":   {in: []error{first, second}, want: first},
		"nil does not overwrite": {in: []error{first, nil}, want: first},
	} {
		t.Run(name, func(t *testing.T) {
			var got error
			for _, err := range tc.in {
				keepFirst(&got, err)
			}
			if !errors.Is(got, tc.want) || (tc.want == nil && got != nil) {
				t.Errorf("keepFirst over %v = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
