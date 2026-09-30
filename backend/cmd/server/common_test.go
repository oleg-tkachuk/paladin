package main

import (
	"context"
	"errors"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/observability"
)

// flushOTel is the shutdown hook every serve/migrate subcommand defers
// (ADR-0001 activation). These smoke tests pin its contract — the wiring
// itself (boot → InitOTel) is exercised by observability.TestInitOTel_*.

func TestFlushOTel_NilIsSafe(t *testing.T) {
	// A nil shutdown (e.g. a future caller that never set one) must no-op,
	// not panic — it runs in a defer on the shutdown path.
	flushOTel(nil)
}

func TestFlushOTel_RunsWithFreshBoundedContext(t *testing.T) {
	// The parent context is already cancelled (SIGTERM fired) by the time
	// the deferred flush runs. flushOTel must hand the hook a *fresh*,
	// bounded context so the final span/metric batch still flushes.
	var (
		called      bool
		sawCancel   bool
		hadDeadline bool
	)
	var sh observability.ShutdownFunc = func(ctx context.Context) error {
		called = true
		sawCancel = ctx.Err() != nil
		_, hadDeadline = ctx.Deadline()
		return nil
	}

	flushOTel(sh)

	if !called {
		t.Fatal("flushOTel did not invoke the shutdown hook")
	}
	if sawCancel {
		t.Error("flushOTel passed an already-cancelled context; the flush could be aborted")
	}
	if !hadDeadline {
		t.Error("flushOTel should bound the flush with a deadline, not pass an open-ended context")
	}
}

func TestFlushOTel_SwallowsError(t *testing.T) {
	// Observability shutdown is best-effort: a flush error must never
	// propagate or panic out of the deferred cleanup.
	var sh observability.ShutdownFunc = func(context.Context) error {
		return errors.New("collector unreachable")
	}
	flushOTel(sh)
}
