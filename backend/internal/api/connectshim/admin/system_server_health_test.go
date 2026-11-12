package admin

import (
	"context"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	pbiam "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

// healthSystem answers PlatformHealth with fixed snapshots.
type healthSystem struct {
	failingSystem
	snaps []health.Snapshot
}

func (s healthSystem) PlatformHealth(context.Context) []health.Snapshot { return s.snaps }

func TestGetPlatformHealthProjectsEachRoleInOrder(t *testing.T) {
	at := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	srv := &SystemServer{H: healthSystem{snaps: []health.Snapshot{
		{Role: "iam", Status: health.StatusHealthy, CheckedAt: at,
			Components: []health.Component{{Name: "postgres", Status: health.StatusHealthy}}},
		{Role: "mcp", Status: health.StatusUnhealthy, CheckedAt: at.Add(time.Second)},
	}}}
	res, err := srv.GetPlatformHealth(t.Context(), &pb.GetPlatformHealthRequest{})
	if err != nil {
		t.Fatalf("GetPlatformHealth: %v", err)
	}
	roles := res.GetRoles()
	if len(roles) != 2 || roles[0].GetHealth().GetRole() != "iam" || roles[1].GetHealth().GetRole() != "mcp" {
		t.Fatalf("roles = %v, want iam then mcp", roles)
	}
	if got := roles[1].GetHealth().GetStatus(); got != pbiam.ComponentStatus_COMPONENT_STATUS_UNHEALTHY {
		t.Errorf("mcp status = %v, want unhealthy", got)
	}
	if got := roles[0].GetHealth().GetComponents(); len(got) != 1 || got[0].GetName() != "postgres" {
		t.Errorf("iam components = %v", got)
	}
	if !roles[1].GetCheckedAt().AsTime().Equal(at.Add(time.Second)) {
		t.Errorf("mcp checked_at = %v, want its own", roles[1].GetCheckedAt().AsTime())
	}
}

func TestGetPlatformHealthUnwiredIsUnavailable(t *testing.T) {
	if _, err := (&SystemServer{}).GetPlatformHealth(t.Context(), &pb.GetPlatformHealthRequest{}); err == nil {
		t.Fatal("an unwired server answered")
	}
}
