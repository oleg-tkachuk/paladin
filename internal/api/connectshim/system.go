package connectshim

import (
	"context"

	"connectrpc.com/connect"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/v1/paladinv1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/system"
)

type SystemServer struct {
	paladinv1connect.UnimplementedSystemServiceHandler
	H *system.Handler
}

func NewSystemServer(h *system.Handler) *SystemServer { return &SystemServer{H: h} }

func (s *SystemServer) GetVersion(ctx context.Context, _ *connect.Request[pb.GetVersionRequest]) (*connect.Response[pb.VersionInfo], error) {
	info := s.H.GetVersion(ctx)
	return connect.NewResponse(&pb.VersionInfo{
		Version:   info.Version,
		Commit:    info.Commit,
		BuildTime: tsProto(info.BuildTime),
		GoVersion: info.GoVersion,
	}), nil
}

func (s *SystemServer) GetHealth(ctx context.Context, _ *connect.Request[pb.GetHealthRequest]) (*connect.Response[pb.HealthInfo], error) {
	h := s.H.GetHealth(ctx)
	out := &pb.HealthInfo{
		Status:    h.Status,
		CheckedAt: tsProto(h.CheckedAt),
	}
	for _, c := range h.Components {
		out.Components = append(out.Components, &pb.ComponentHealth{
			Name:      c.Name,
			Status:    c.Status,
			Message:   c.Message,
			LatencyMs: c.LatencyMS,
		})
	}
	return connect.NewResponse(out), nil
}

var _ paladinv1connect.SystemServiceHandler = (*SystemServer)(nil)
