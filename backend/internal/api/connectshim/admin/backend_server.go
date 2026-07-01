package admin

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/backendh"
	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1/paladinadminv1connect"
)

type BackendServer struct {
	paladinadminv1connect.UnimplementedBackendServiceHandler
	H *backendh.Handler
}

func NewBackendServer(h *backendh.Handler) *BackendServer { return &BackendServer{H: h} }

func (s *BackendServer) CreateBackend(ctx context.Context, req *connect.Request[pb.CreateBackendRequest]) (*connect.Response[pb.StorageBackend], error) {
	m := req.Msg
	b := backendFromProto(m.GetBackend())
	if b.BackendID == "" {
		b.BackendID = m.GetBackendId()
	}
	out, err := s.H.CreateBackend(ctx, b)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(backendToProto(out)), nil
}

func (s *BackendServer) GetBackend(ctx context.Context, req *connect.Request[pb.GetBackendRequest]) (*connect.Response[pb.StorageBackend], error) {
	id, err := backendIDFromName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.GetBackend(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(backendToProto(out)), nil
}

func (s *BackendServer) ListBackends(ctx context.Context, req *connect.Request[pb.ListBackendsRequest]) (*connect.Response[pb.ListBackendsResponse], error) {
	m := req.Msg
	list, next, err := s.H.ListBackends(ctx, m.GetPage().GetPageSize(), m.GetPage().GetPageToken())
	if err != nil {
		return nil, err
	}
	out := &pb.ListBackendsResponse{Page: pageResponseProto(next)}
	for i := range list {
		out.Backends = append(out.Backends, backendToProto(&list[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *BackendServer) UpdateBackend(ctx context.Context, req *connect.Request[pb.UpdateBackendRequest]) (*connect.Response[pb.StorageBackend], error) {
	m := req.Msg
	id, err := backendIDFromName(m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseRV(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	b := backendFromProto(m.GetBackend())
	b.BackendID = id
	out, err := s.H.UpdateBackend(ctx, b, rv, m.GetUpdateMask().GetPaths())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(backendToProto(out)), nil
}

func (s *BackendServer) DeleteBackend(ctx context.Context, req *connect.Request[pb.DeleteBackendRequest]) (*connect.Response[pb.DeleteBackendResponse], error) {
	id, err := backendIDFromName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseRV(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// OCC contract: rv=0 only allowed with explicit force=true.
	if rv == 0 && !req.Msg.GetForce() {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("resource_version required; pass force=true to bypass"))
	}
	if err := s.H.DeleteBackend(ctx, id, rv, req.Msg.GetForce()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.DeleteBackendResponse{}), nil
}

func (s *BackendServer) SetBackendEnabled(ctx context.Context, req *connect.Request[pb.SetBackendEnabledRequest]) (*connect.Response[pb.StorageBackend], error) {
	id, err := backendIDFromName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseRV(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// OCC contract: a state flip must carry the current version. Unlike
	// DeleteBackend there is no force escape — rv=0 is rejected so the
	// flip can never silently clobber a concurrent change.
	if rv == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("resource_version required"))
	}
	out, err := s.H.SetBackendEnabled(ctx, id, req.Msg.GetEnabled(), rv)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(backendToProto(out)), nil
}

func (s *BackendServer) SetBackendReadOnly(ctx context.Context, req *connect.Request[pb.SetBackendReadOnlyRequest]) (*connect.Response[pb.StorageBackend], error) {
	id, err := backendIDFromName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseRV(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// Same OCC contract as SetBackendEnabled: rv=0 is rejected so a drain
	// flip can never clobber a concurrent change.
	if rv == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("resource_version required"))
	}
	out, err := s.H.SetBackendReadOnly(ctx, id, req.Msg.GetReadOnly(), rv)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(backendToProto(out)), nil
}

func (s *BackendServer) RotateCredentials(ctx context.Context, req *connect.Request[pb.RotateCredentialsRequest]) (*connect.Response[pb.StorageBackend], error) {
	id, err := backendIDFromName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// grace_period is a Go duration string ("30m", "1h"); empty = instant.
	var grace time.Duration
	if gp := req.Msg.GetGracePeriod(); gp != "" {
		d, perr := time.ParseDuration(gp)
		if perr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("invalid grace_period %q: %w", gp, perr))
		}
		grace = d
	}
	out, err := s.H.RotateCredentials(ctx, id, req.Msg.GetNewSecretRef(), grace)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(backendToProto(out)), nil
}

func (s *BackendServer) TestBackend(ctx context.Context, req *connect.Request[pb.TestBackendRequest]) (*connect.Response[pb.TestBackendResponse], error) {
	id, err := backendIDFromName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.TestBackend(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.TestBackendResponse{
		Reachable:    out.Reachable,
		ErrorMessage: out.ErrorMessage,
		LatencyMs:    out.LatencyMs,
	}), nil
}

var _ paladinadminv1connect.BackendServiceHandler = (*BackendServer)(nil)
