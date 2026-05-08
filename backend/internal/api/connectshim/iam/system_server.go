package iam

import (
	"context"
	"errors"
	"runtime"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/internal/health"
)

// SystemServer implements paladin.iam.v1.SystemService.
//
// It surfaces process build metadata (filled at link time via -ldflags)
// and per-component health, reusing the same `health.Handler.Ready`
// checks the K8s probes run. Lives on the IAM plane so any authenticated
// caller can read it — the response carries no tenant-sensitive data.
type SystemServer struct {
	paladiniamv1connect.UnimplementedSystemServiceHandler

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
	_ *connect.Request[pb.GetVersionRequest],
) (*connect.Response[pb.VersionInfo], error) {
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
	return connect.NewResponse(out), nil
}

func (s *SystemServer) GetHealth(
	ctx context.Context,
	_ *connect.Request[pb.GetHealthRequest],
) (*connect.Response[pb.HealthInfo], error) {
	if s.Health == nil {
		// No checks registered → trivially healthy. Useful in tests; the
		// production wiring always passes a real *health.Handler.
		return connect.NewResponse(&pb.HealthInfo{
			Status: pb.ComponentStatus_COMPONENT_STATUS_HEALTHY,
		}), nil
	}

	timeout := s.Health.Timeout
	if timeout <= 0 {
		timeout = health.DefaultCheckTimeout
	}

	components := make([]*pb.ComponentHealth, 0, len(s.Health.Ready))
	worst := pb.ComponentStatus_COMPONENT_STATUS_HEALTHY
	for _, c := range s.Health.Ready {
		cctx, cancel := context.WithTimeout(ctx, timeout)
		start := time.Now()
		err := c.Func(cctx)
		latency := time.Since(start)
		cancel()

		ch := &pb.ComponentHealth{
			Name:      c.Name,
			LatencyMs: latency.Milliseconds(),
			Category:  string(c.Category),
			Critical:  c.Critical,
		}
		switch {
		case err == nil:
			ch.Status = pb.ComponentStatus_COMPONENT_STATUS_HEALTHY
		case errors.Is(err, context.DeadlineExceeded):
			ch.Status = pb.ComponentStatus_COMPONENT_STATUS_UNHEALTHY
			ch.Message = "check timed out after " + timeout.String()
		default:
			ch.Status = pb.ComponentStatus_COMPONENT_STATUS_UNHEALTHY
			ch.Message = err.Error()
		}

		// Aggregate is asymmetric on Critical: a non-critical
		// UNHEALTHY component drops the roll-up to DEGRADED instead
		// of UNHEALTHY, so the UI can distinguish "runtime broken"
		// from "informational dep flapping". Matches /readyz: only
		// critical failures take the pod out of the endpoint set.
		switch {
		case ch.Status == pb.ComponentStatus_COMPONENT_STATUS_UNHEALTHY && c.Critical:
			worst = pb.ComponentStatus_COMPONENT_STATUS_UNHEALTHY
		case ch.Status == pb.ComponentStatus_COMPONENT_STATUS_UNHEALTHY:
			if worst < pb.ComponentStatus_COMPONENT_STATUS_DEGRADED {
				worst = pb.ComponentStatus_COMPONENT_STATUS_DEGRADED
			}
		case ch.Status > worst:
			worst = ch.Status
		}
		components = append(components, ch)
	}

	return connect.NewResponse(&pb.HealthInfo{
		Status:     worst,
		Components: components,
		Role:       s.Role,
	}), nil
}
