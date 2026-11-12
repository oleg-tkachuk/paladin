package healthpb

import (
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

// ─── health projection ─────────────────────────────────────────────────────

func TestStatusToProto(t *testing.T) {
	cases := map[health.ComponentStatus]pb.ComponentStatus{
		health.StatusHealthy:   pb.ComponentStatus_COMPONENT_STATUS_HEALTHY,
		health.StatusDegraded:  pb.ComponentStatus_COMPONENT_STATUS_DEGRADED,
		health.StatusUnhealthy: pb.ComponentStatus_COMPONENT_STATUS_UNHEALTHY,
		health.StatusDisabled:  pb.ComponentStatus_COMPONENT_STATUS_DISABLED,
		// Anything unrecognised must be UNSPECIFIED rather than defaulting to
		// healthy — a probe bug must never read as "all good".
		health.ComponentStatus("weird"): pb.ComponentStatus_COMPONENT_STATUS_UNSPECIFIED,
		health.ComponentStatus(""):      pb.ComponentStatus_COMPONENT_STATUS_UNSPECIFIED,
	}
	for in, want := range cases {
		if got := statusToProto(in); got != want {
			t.Errorf("statusToProto(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestControlToProto(t *testing.T) {
	cases := map[health.Control]pb.ComponentControl{
		health.ControlAlwaysOn: pb.ComponentControl_COMPONENT_CONTROL_ALWAYS_ON,
		health.ControlConfig:   pb.ComponentControl_COMPONENT_CONTROL_CONFIG,
		health.ControlDatabase: pb.ComponentControl_COMPONENT_CONTROL_DATABASE,
		health.Control(""):     pb.ComponentControl_COMPONENT_CONTROL_UNSPECIFIED,
	}
	for in, want := range cases {
		if got := controlToProto(in); got != want {
			t.Errorf("controlToProto(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestFromSnapshot(t *testing.T) {
	s := health.Snapshot{
		Role:   "api",
		Status: health.StatusDegraded,
		Components: []health.Component{
			{Name: "postgres", Status: health.StatusHealthy, LatencyMs: 3, Category: "db", Critical: true},
			{Name: "s3", Status: health.StatusUnhealthy, Message: "timeout", Category: "storage"},
			{Name: "nats", Status: health.StatusDisabled, Message: "not in use", Control: health.ControlDatabase},
			{Name: "postgres-schema", Status: health.StatusHealthy, Details: []health.Detail{
				{Name: "schema applied", Value: "56"}, {Name: "schema this build needs", Value: "56"},
			}},
		},
	}

	got := FromSnapshot(s)

	if got.Role != "api" {
		t.Errorf("Role = %q", got.Role)
	}
	if got.Status != statusToProto(health.StatusDegraded) {
		t.Errorf("Status = %v", got.Status)
	}
	if len(got.Components) != 4 {
		t.Fatalf("got %d components, want 4", len(got.Components))
	}
	if d := got.Components[3].Details; len(d) != 2 || d[0].Name != "schema applied" || d[1].Value != "56" {
		t.Errorf("component[3] details = %v, want both, in order", d)
	}
	if got.Components[0].Details != nil {
		t.Errorf("component[0] details = %v, want none", got.Components[0].Details)
	}
	if c := got.Components[2]; c.Control != pb.ComponentControl_COMPONENT_CONTROL_DATABASE || c.Message != "not in use" {
		t.Errorf("component[2] = %+v, want its switch carried over", c)
	}
	if got.Components[0].Name != "postgres" || got.Components[0].LatencyMs != 3 ||
		!got.Components[0].Critical || got.Components[0].Category != "db" {
		t.Errorf("component[0] = %+v", got.Components[0])
	}
	if got.Components[1].Message != "timeout" {
		t.Errorf("component[1] message = %q", got.Components[1].Message)
	}
}

func TestFromSnapshotNoComponents(t *testing.T) {
	got := FromSnapshot(health.Snapshot{Role: "worker", Status: health.StatusHealthy})
	if got.Components == nil {
		t.Error("Components must be an empty slice, not nil")
	}
	if len(got.Components) != 0 {
		t.Errorf("want no components, got %d", len(got.Components))
	}
}
