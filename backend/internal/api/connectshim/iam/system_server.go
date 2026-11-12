package iam

import (
	"context"
	"runtime"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	"github.com/oleg-tkachuk/paladin/backend/internal/health/healthpb"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// SystemServer implements paladin.iam.v1.SystemService.
//
// It surfaces process build metadata (filled at link time via -ldflags)
// and per-component health, reusing the same `health.Handler.Ready`
// checks the K8s probes run. Lives on the IAM plane so any authenticated
// caller can read it — the response carries no tenant-sensitive data.
type SystemServer struct {
	paladiniamv1connect.UnimplementedHealthServiceHandler

	// Build metadata. Empty/zero in local `go run` builds.
	Version   string
	Commit    string
	BuildTime time.Time
	// GoVersion defaults to runtime.Version() when empty.
	GoVersion string

	// Role labels the GetHealth response so the UI can attribute a
	// degraded check to the binary that ran it ("api", "admin",
	// "worker", "mcp"). The api binary opens both data + iam planes
	// in one process; both record `role=api`.
	Role string

	// Health gives us the registered Ready checks. Each check carries a
	// bounded-timeout func; we run them with their own per-call timer to
	// surface latency to the UI.
	Health *health.Handler
}

func NewSystemServer(version, commit string, buildTime time.Time, role string, h *health.Handler) *SystemServer {
	return &SystemServer{
		Version:   version,
		Commit:    commit,
		BuildTime: buildTime,
		GoVersion: runtime.Version(),
		Role:      role,
		Health:    h,
	}
}

func (s *SystemServer) GetVersion(
	_ context.Context,
	_ *pb.GetVersionRequest,
) (*pb.VersionInfo, error) {
	out := &pb.VersionInfo{
		Version:   s.Version,
		Commit:    s.Commit,
		GoVersion: s.GoVersion,
	}
	if !s.BuildTime.IsZero() {
		out.BuildTime = timestamppb.New(s.BuildTime)
	}
	if out.GoVersion == "" {
		out.GoVersion = runtime.Version()
	}
	return out, nil
}

func (s *SystemServer) GetHealth(
	ctx context.Context,
	_ *pb.GetHealthRequest,
) (*pb.HealthInfo, error) {
	if s.Health == nil {
		// No checks registered → trivially healthy. Useful in tests; the
		// production wiring always passes a real *health.Handler.
		return &pb.HealthInfo{
			Status: pb.ComponentStatus_COMPONENT_STATUS_HEALTHY,
			Role:   s.Role,
		}, nil
	}
	snap := s.Health.Snapshot(ctx, s.Role)
	return healthpb.FromSnapshot(snap), nil
}
