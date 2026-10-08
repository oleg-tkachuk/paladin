package health

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// failIfRun stands in for a disabled component's probe: it must never run.
func failIfRun(t *testing.T) func(context.Context) error {
	return func(context.Context) error {
		t.Error("a disabled check was run")
		return errors.New("ran")
	}
}

// A component off by configuration was reported healthy, with a "disabled"
// note, as if probed: a green row with a latency for nothing measured.
func TestSnapshotListsADisabledComponentAsDisabled(t *testing.T) {
	t.Parallel()
	h := &Handler{Ready: []Check{
		{Name: "postgres", Critical: true, Func: func(context.Context) error { return nil }},
		{Name: "postgres-replica", Category: CategoryDatabase, Disabled: true, Func: failIfRun(t)},
	}}
	snap := h.Snapshot(context.Background(), "api")

	if len(snap.Components) != 2 {
		t.Fatalf("components = %+v, want both listed", snap.Components)
	}
	got := snap.Components[1]
	if got.Name != "postgres-replica" || got.Status != StatusDisabled {
		t.Errorf("component = %+v, want postgres-replica disabled", got)
	}
	if got.Message != "" || got.LatencyMs != 0 || got.Category != string(CategoryDatabase) {
		t.Errorf("component = %+v, want no message, no latency, its category", got)
	}
	if snap.Status != StatusHealthy {
		t.Errorf("role status = %s, want healthy: disabled is not a fault", snap.Status)
	}
}

// Disabled never reaches a role's status, whatever else the role reports.
func TestADisabledComponentLeavesTheRollupToTheOthers(t *testing.T) {
	t.Parallel()
	h := &Handler{Ready: []Check{
		{Name: "cache", Func: func(context.Context) error { return errors.New("down") }},
		{Name: "ingest", Disabled: true},
	}}
	if got := h.Snapshot(context.Background(), "api").Status; got != StatusDegraded {
		t.Errorf("role status = %s, want degraded from the failing non-critical check", got)
	}
}

func TestReadyzSkipsADisabledComponent(t *testing.T) {
	t.Parallel()
	h := &Handler{Ready: []Check{
		{Name: "ingest", Critical: true, Disabled: true, Func: failIfRun(t)},
	}}
	code, body := probe(t, h, "/readyz")
	if code != http.StatusOK || len(body.Failures) != 0 {
		t.Errorf("readyz = %d %+v, want 200 with no failures", code, body.Failures)
	}
}
