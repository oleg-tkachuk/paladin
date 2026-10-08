package health

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// A broker no subscription delivers to was probed by an empty pool and
// reported healthy. Nothing to probe is not in use, shown disabled with why.
func TestANotInUseComponentIsDisabledWithItsReason(t *testing.T) {
	t.Parallel()
	const reason = "no enabled subscription delivers to rabbitmq"
	h := &Handler{Ready: []Check{
		{Name: "rabbitmq", Func: func(context.Context) error { return NotInUse(reason) }},
	}}
	snap := h.Snapshot(context.Background(), "dispatcher")

	got := snap.Components[0]
	if got.Status != StatusDisabled || got.Message != reason {
		t.Errorf("component = %+v, want disabled with %q", got, reason)
	}
	if snap.Status != StatusHealthy {
		t.Errorf("role status = %s, want healthy: not in use is not a fault", snap.Status)
	}
}

// Wrapped, it is still recognised: a check may add context to it.
func TestAWrappedNotInUseIsStillDisabled(t *testing.T) {
	t.Parallel()
	h := &Handler{Ready: []Check{{
		Name: "nats",
		Func: func(context.Context) error { return fmt.Errorf("nats: %w", NotInUse("unused")) },
	}}}
	if got := h.Snapshot(context.Background(), "dispatcher").Components[0].Status; got != StatusDisabled {
		t.Errorf("status = %s, want disabled", got)
	}
}

// /readyz agrees with the snapshot: not in use fails nothing, even critical.
func TestReadyzPassesANotInUseComponent(t *testing.T) {
	t.Parallel()
	h := &Handler{Ready: []Check{{
		Name:     "nats",
		Critical: true,
		Func:     func(context.Context) error { return NotInUse("unused") },
	}}}
	code, body := probe(t, h, "/readyz")
	if code != http.StatusOK || len(body.Failures) != 0 {
		t.Errorf("readyz = %d %+v, want 200 with no failures", code, body.Failures)
	}
}
