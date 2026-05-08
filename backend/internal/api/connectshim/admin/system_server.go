package admin

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/systemh"
	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1/paladinadminv1connect"
)

// SystemServer adapts *systemh.Handler to the generated Connect
// interface. Authn/role gating lives inside the handler so the
// connectshim coverage test sees a gated call site.
type SystemServer struct {
	paladinadminv1connect.UnimplementedSystemServiceHandler
	H *systemh.Handler
}

func NewSystemServer(h *systemh.Handler) *SystemServer {
	return &SystemServer{H: h}
}

func (s *SystemServer) GetConfig(ctx context.Context, _ *connect.Request[pb.GetConfigRequest]) (*connect.Response[pb.GetConfigResponse], error) {
	if s.H == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("system handler not wired"))
	}
	yamlBlob, sourcePath, err := s.H.MarshalRedacted(ctx)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.GetConfigResponse{
		Yaml:       yamlBlob,
		SourcePath: sourcePath,
	}), nil
}
