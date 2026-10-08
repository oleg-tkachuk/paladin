package admin

import (
	"context"
	"fmt"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/backendh"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
)

type BackendServer struct {
	paladinadminv1connect.UnimplementedBackendServiceHandler
	H backendHandler
}

func NewBackendServer(h *backendh.Handler) *BackendServer { return &BackendServer{H: h} }

func (s *BackendServer) CreateBackend(ctx context.Context, req *pb.CreateBackendRequest) (*pb.StorageBackend, error) {
	m := req
	b := backendFromProto(m.GetBackend())
	if b.BackendID == "" {
		b.BackendID = m.GetBackendId()
	}
	out, err := s.H.CreateBackend(ctx, b)
	if err != nil {
		return nil, err
	}
	return backendToProto(out), nil
}

func (s *BackendServer) GetBackend(ctx context.Context, req *pb.GetBackendRequest) (*pb.StorageBackend, error) {
	id, err := backendIDFromName(req.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	out, err := s.H.GetBackend(ctx, id)
	if err != nil {
		return nil, err
	}
	return backendToProto(out), nil
}

func (s *BackendServer) ListBackends(ctx context.Context, req *pb.ListBackendsRequest) (*pb.ListBackendsResponse, error) {
	m := req
	list, next, err := s.H.ListBackends(ctx, m.GetPage().GetPageSize(), m.GetPage().GetPageToken(), m.GetFilter())
	if err != nil {
		return nil, err
	}
	out := &pb.ListBackendsResponse{Page: convx.PageResponseProto(next)}
	for i := range list {
		out.Backends = append(out.Backends, backendToProto(&list[i]))
	}
	return out, nil
}

// updateBackendPaths are the StorageBackend fields UpdateBackend applies.
var updateBackendPaths = []string{
	"display_name", "endpoint", "public_endpoint", "region", "force_path_style",
	"credentials_secret_ref", "sse", "events", "cedar_policy",
}

func (s *BackendServer) UpdateBackend(ctx context.Context, req *pb.UpdateBackendRequest) (*pb.StorageBackend, error) {
	m := req
	id, err := backendIDFromName(m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	rv, err := convx.ParseRV(m.GetResourceVersion())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err))
	}
	if err := convx.CheckMask(m.GetUpdateMask().GetPaths(), updateBackendPaths); err != nil {
		return nil, err
	}
	b := backendFromProto(m.GetBackend())
	b.BackendID = id
	out, err := s.H.UpdateBackend(ctx, b, rv, m.GetUpdateMask().GetPaths())
	if err != nil {
		return nil, err
	}
	return backendToProto(out), nil
}

func (s *BackendServer) DeleteBackend(ctx context.Context, req *pb.DeleteBackendRequest) (*pb.DeleteBackendResponse, error) {
	id, err := backendIDFromName(req.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	rv, err := convx.ParseRV(req.GetResourceVersion())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err))
	}
	// OCC contract. There is no opt-out on this RPC: the old `force` claimed
	// to delete a backend buckets still reference, which the RESTRICT foreign
	// key refuses anyway, so the flag bought nothing and cost the guard.
	if rv == 0 {
		return nil, connect.Errorf(connect.CodeInvalidArgument,
			"resource_version is required")
	}
	if err := s.H.DeleteBackend(ctx, id, rv); err != nil {
		return nil, err
	}
	return &pb.DeleteBackendResponse{}, nil
}

func (s *BackendServer) SetBackendEnabled(ctx context.Context, req *pb.SetBackendEnabledRequest) (*pb.StorageBackend, error) {
	id, err := backendIDFromName(req.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	rv, err := convx.ParseRV(req.GetResourceVersion())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err))
	}
	// OCC contract: a state flip must carry the current version. Unlike
	// DeleteBackend there is no force escape — rv=0 is rejected so the
	// flip can never silently clobber a concurrent change.
	if rv == 0 {
		return nil, connect.Errorf(connect.CodeInvalidArgument,
			"resource_version required")
	}
	out, err := s.H.SetBackendEnabled(ctx, id, req.GetEnabled(), rv)
	if err != nil {
		return nil, err
	}
	return backendToProto(out), nil
}

func (s *BackendServer) SetBackendReadOnly(ctx context.Context, req *pb.SetBackendReadOnlyRequest) (*pb.StorageBackend, error) {
	id, err := backendIDFromName(req.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	rv, err := convx.ParseRV(req.GetResourceVersion())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err))
	}
	// Same OCC contract as SetBackendEnabled: rv=0 is rejected so a drain
	// flip can never clobber a concurrent change.
	if rv == 0 {
		return nil, connect.Errorf(connect.CodeInvalidArgument,
			"resource_version required")
	}
	out, err := s.H.SetBackendReadOnly(ctx, id, req.GetReadOnly(), rv)
	if err != nil {
		return nil, err
	}
	return backendToProto(out), nil
}

func (s *BackendServer) SetBackendMaintenance(ctx context.Context, req *pb.SetBackendMaintenanceRequest) (*pb.StorageBackend, error) {
	id, err := backendIDFromName(req.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	rv, err := convx.ParseRV(req.GetResourceVersion())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err))
	}
	if rv == 0 {
		return nil, connect.Errorf(connect.CodeInvalidArgument,
			"resource_version required")
	}
	out, err := s.H.SetBackendMaintenance(ctx, id, req.GetMaintenance(), rv)
	if err != nil {
		return nil, err
	}
	return backendToProto(out), nil
}

func (s *BackendServer) RotateCredentials(ctx context.Context, req *pb.RotateCredentialsRequest) (*pb.StorageBackend, error) {
	id, err := backendIDFromName(req.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	// grace_period is a Go duration string ("30m", "1h"); empty = instant.
	var grace time.Duration
	if gp := req.GetGracePeriod(); gp != "" {
		d, perr := time.ParseDuration(gp)
		if perr != nil {
			return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid grace_period %q: %w", gp, perr))
		}
		grace = d
	}
	out, err := s.H.RotateCredentials(ctx, id, req.GetNewSecretRef(), grace)
	if err != nil {
		return nil, err
	}
	return backendToProto(out), nil
}

func (s *BackendServer) TestBackend(ctx context.Context, req *pb.TestBackendRequest) (*pb.TestBackendResponse, error) {
	id, err := backendIDFromName(req.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	out, err := s.H.TestBackend(ctx, id)
	if err != nil {
		return nil, err
	}
	resp := &pb.TestBackendResponse{
		Reachable:    out.Reachable,
		ErrorMessage: out.ErrorMessage,
		LatencyMs:    out.LatencyMs,
	}
	if out.Features != nil {
		resp.Features = featuresToProto(out.Features)
		resp.Compatibility = compatibilityToProto(out.Features)
	}
	return resp, nil
}

var _ paladinadminv1connect.BackendServiceHandler = (*BackendServer)(nil)
