// Package healthpb converts health snapshots into the proto the iam plane's
// HealthService and the admin plane's GetPlatformHealth both publish, so the
// two cannot drift.
package healthpb

import (
	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

// FromSnapshot converts the canonical health.Snapshot into the proto
// twin SystemService publishes. Keeping this in one place ensures the
// authenticated RPC and the unauthenticated /system/health.json endpoint
// can never drift on aggregation semantics — both are derived from the
// same Snapshot run.
func FromSnapshot(s health.Snapshot) *pb.HealthInfo {
	out := &pb.HealthInfo{
		Status:     statusToProto(s.Status),
		Components: make([]*pb.ComponentHealth, 0, len(s.Components)),
		Role:       s.Role,
	}
	for _, c := range s.Components {
		out.Components = append(out.Components, &pb.ComponentHealth{
			Name:      c.Name,
			Status:    statusToProto(c.Status),
			Message:   c.Message,
			LatencyMs: c.LatencyMs,
			Category:  c.Category,
			Critical:  c.Critical,
			Control:   controlToProto(c.Control),
			Details:   detailsToProto(c.Details),
		})
	}
	return out
}

// detailsToProto keeps a component's details in their order.
func detailsToProto(ds []health.Detail) []*pb.ComponentDetail {
	if len(ds) == 0 {
		return nil
	}
	out := make([]*pb.ComponentDetail, 0, len(ds))
	for _, d := range ds {
		out = append(out, &pb.ComponentDetail{Name: d.Name, Value: d.Value, Group: d.Group})
	}
	return out
}

// controlToProto maps where a component's switch lives.
func controlToProto(c health.Control) pb.ComponentControl {
	switch c {
	case health.ControlAlwaysOn:
		return pb.ComponentControl_COMPONENT_CONTROL_ALWAYS_ON
	case health.ControlConfig:
		return pb.ComponentControl_COMPONENT_CONTROL_CONFIG
	case health.ControlDatabase:
		return pb.ComponentControl_COMPONENT_CONTROL_DATABASE
	default:
		return pb.ComponentControl_COMPONENT_CONTROL_UNSPECIFIED
	}
}

func statusToProto(s health.ComponentStatus) pb.ComponentStatus {
	switch s {
	case health.StatusHealthy:
		return pb.ComponentStatus_COMPONENT_STATUS_HEALTHY
	case health.StatusDegraded:
		return pb.ComponentStatus_COMPONENT_STATUS_DEGRADED
	case health.StatusUnhealthy:
		return pb.ComponentStatus_COMPONENT_STATUS_UNHEALTHY
	case health.StatusDisabled:
		return pb.ComponentStatus_COMPONENT_STATUS_DISABLED
	default:
		return pb.ComponentStatus_COMPONENT_STATUS_UNSPECIFIED
	}
}
