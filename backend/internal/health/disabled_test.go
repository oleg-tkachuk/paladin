package health

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// failIfRun stands in for a disabled component's check: it must never run.
func failIfRun(t *testing.T) func(context.Context) error {
	t.Helper()
	return func(context.Context) error {
		t.Error("a disabled component was checked")
		return errors.New("ran")
	}
}

func ok(context.Context) error { return nil }

// A component off by configuration was reported healthy, with a "disabled"
// note, as if probed: a green row with a latency for nothing measured.
func TestSnapshotListsAComponentOffByConfigAsDisabled(t *testing.T) {
	t.Parallel()
	h := &Handler{Ready: []Probe{
		Check{Name: "postgres", Critical: true, Func: ok},
		Check{
			Name:     "postgres-replica",
			Category: CategoryDatabase,
			Switch:   Fixed(ByConfig(false, "datastores.postgres.replica.enabled")),
			Func:     failIfRun(t),
		},
	}}
	snap := h.Snapshot(context.Background(), "api")

	if len(snap.Components) != 2 {
		t.Fatalf("components = %+v, want both listed", snap.Components)
	}
	got := snap.Components[1]
	want := Component{
		Name:     "postgres-replica",
		Status:   StatusDisabled,
		Message:  "off by configuration: datastores.postgres.replica.enabled",
		Category: string(CategoryDatabase),
		Control:  ControlConfig,
	}
	if got != want {
		t.Errorf("component = %+v, want %+v", got, want)
	}
	if snap.Status != StatusHealthy {
		t.Errorf("role status = %s, want healthy: disabled is not a fault", snap.Status)
	}
}

// A broker no subscription delivers to was probed by an empty pool and
// reported healthy. Switched by the database, it is disabled with why.
func TestSnapshotListsAComponentNothingUsesAsDisabled(t *testing.T) {
	t.Parallel()
	h := &Handler{Ready: []Probe{Check{
		Name:   "rabbitmq",
		Switch: Fixed(ByDatabase(false, "no enabled subscription delivers to rabbitmq")),
		Func:   failIfRun(t),
	}}}
	got := h.Snapshot(context.Background(), "dispatcher").Components[0]
	if got.Status != StatusDisabled || got.Control != ControlDatabase ||
		got.Message != "not in use: no enabled subscription delivers to rabbitmq" {
		t.Errorf("component = %+v, want disabled by the database, with why", got)
	}
}

// Each kind of switch is reported, on or off, so the page can say what
// turns a component on.
func TestSnapshotReportsWhereEachSwitchLives(t *testing.T) {
	t.Parallel()
	h := &Handler{Ready: []Probe{
		Check{Name: "postgres", Func: ok},
		Check{Name: "capability", Switch: Fixed(ByConfig(true, "capability.enabled")), Func: ok},
		Check{Name: "nats", Switch: Fixed(ByDatabase(true, "")), Func: ok},
	}}
	want := []Control{ControlAlwaysOn, ControlConfig, ControlDatabase}
	for i, c := range h.Snapshot(context.Background(), "api").Components {
		if c.Control != want[i] || c.Status != StatusHealthy || c.Message != "" {
			t.Errorf("%s = %+v, want healthy, control %s", c.Name, c, want[i])
		}
	}
}

// A switch that cannot be read is the component's failure: nothing says it
// is off, and nothing checked it is up.
func TestASwitchThatCannotBeReadFailsTheComponent(t *testing.T) {
	t.Parallel()
	h := &Handler{Ready: []Probe{Check{
		Name: "nats",
		Switch: func(context.Context) (Enablement, error) {
			return Enablement{Control: ControlDatabase}, errors.New("permission denied")
		},
		Func: failIfRun(t),
	}}}
	snap := h.Snapshot(context.Background(), "dispatcher")
	got := snap.Components[0]
	if got.Status != StatusUnhealthy || !strings.Contains(got.Message, "permission denied") || got.Control != ControlDatabase {
		t.Errorf("component = %+v, want unhealthy with the read error", got)
	}
}

// Disabled never reaches a role's status, whatever else the role reports.
func TestADisabledComponentLeavesTheRollupToTheOthers(t *testing.T) {
	t.Parallel()
	h := &Handler{Ready: []Probe{
		Check{Name: "cache", Func: func(context.Context) error { return errors.New("down") }},
		Check{Name: "ingest", Switch: Fixed(ByConfig(false, "ingest.enabled"))},
	}}
	if got := h.Snapshot(context.Background(), "api").Status; got != StatusDegraded {
		t.Errorf("role status = %s, want degraded from the failing non-critical check", got)
	}
}

// /readyz agrees with the snapshot: an off component fails nothing, even a
// critical one, and is never checked.
func TestReadyzSkipsADisabledComponent(t *testing.T) {
	t.Parallel()
	h := &Handler{Ready: []Probe{
		Check{Name: "ingest", Critical: true, Switch: Fixed(ByConfig(false, "ingest.enabled")), Func: failIfRun(t)},
		Check{Name: "nats", Critical: true, Switch: Fixed(ByDatabase(false, "unused")), Func: failIfRun(t)},
	}}
	code, body := probe(t, h, "/readyz")
	if code != http.StatusOK || len(body.Failures) != 0 {
		t.Errorf("readyz = %d %+v, want 200 with no failures", code, body.Failures)
	}
}
