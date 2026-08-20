package admin

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/bucketh"
	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1/paladinadminv1connect"
)

type BucketServer struct {
	paladinadminv1connect.UnimplementedBucketServiceHandler
	H *bucketh.Handler
}

func NewBucketServer(h *bucketh.Handler) *BucketServer { return &BucketServer{H: h} }

func (s *BucketServer) CreateBucket(ctx context.Context, req *connect.Request[pb.CreateBucketRequest]) (*connect.Response[pb.Bucket], error) {
	m := req.Msg
	backend, err := backendIDFromName(m.GetParent())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	src := m.GetBucket()
	b := admindomain.Bucket{
		BackendID:   backend,
		BucketName:  m.GetBucketId(),
		DisplayName: src.GetDisplayName(),
		Region:      src.GetRegion(),
		Labels:      src.GetLabels(),
		CedarPolicy: src.GetCedarPolicy(),
		Constraints: constraintsFromProto(src.GetConstraints()),
	}
	if t := src.GetOwnerTenantId(); t != "" {
		if id, perr := uuid.Parse(t); perr == nil {
			b.OwnerTenantID = id
		}
	}
	out, err := s.H.CreateBucket(ctx, bucketh.CreateBucketInput{
		Bucket:             b,
		ProvisionOnBackend: m.GetProvisionOnBackend(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(bucketToProto(out)), nil
}

func (s *BucketServer) GetBucket(ctx context.Context, req *connect.Request[pb.GetBucketRequest]) (*connect.Response[pb.Bucket], error) {
	backend, name, err := bucketNameParts(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.GetBucket(ctx, backend, name)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(bucketToProto(out)), nil
}

func (s *BucketServer) ListBuckets(ctx context.Context, req *connect.Request[pb.ListBucketsRequest]) (*connect.Response[pb.ListBucketsResponse], error) {
	m := req.Msg
	args := admindomain.ListBucketsArgs{
		PageSize: m.GetPage().GetPageSize(),
	}
	if m.GetParent() != "" {
		if backend, err := backendIDFromName(m.GetParent()); err == nil {
			args.BackendID = backend
		}
	}
	// owner_tenant_id accepts UUID or slug (consistent with the
	// `tenants/{tenant_id_or_slug}` resource-name convention used by
	// every other RPC). Slug → UUID resolution would need a tenant
	// repo handle in this shim; for now we only accept UUID form
	// here and rely on the frontend (which has the tenant context)
	// to pass UUID. Empty = no filter (platform-admin path).
	if owner := m.GetOwnerTenantId(); owner != "" {
		id, err := uuid.Parse(owner)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("owner_tenant_id must be a UUID: %w", err))
		}
		args.OwnerTenantID = &id
	}
	if tok := m.GetPage().GetPageToken(); tok != "" {
		args.AfterBackend, args.AfterName = splitBucketCursor(tok)
	}
	list, next, err := s.H.ListBuckets(ctx, args)
	if err != nil {
		return nil, err
	}
	out := &pb.ListBucketsResponse{Page: pageResponseProto(next)}
	for i := range list {
		out.Buckets = append(out.Buckets, bucketToProto(&list[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *BucketServer) ListAccessibleBuckets(ctx context.Context, req *connect.Request[pb.ListAccessibleBucketsRequest]) (*connect.Response[pb.ListBucketsResponse], error) {
	m := req.Msg
	tID, err := tenantIDFromName(m.GetTenant())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	id, err := uuid.Parse(tID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	afterBackend, afterName := splitBucketCursor(m.GetPage().GetPageToken())
	list, next, err := s.H.ListAccessibleBuckets(ctx, id, m.GetPage().GetPageSize(), afterBackend, afterName)
	if err != nil {
		return nil, err
	}
	out := &pb.ListBucketsResponse{Page: pageResponseProto(next)}
	for i := range list {
		out.Buckets = append(out.Buckets, bucketToProto(&list[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *BucketServer) UpdateBucket(ctx context.Context, req *connect.Request[pb.UpdateBucketRequest]) (*connect.Response[pb.Bucket], error) {
	m := req.Msg
	backend, name, err := bucketNameParts(m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseRV(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	b := admindomain.Bucket{
		BackendID:   backend,
		BucketName:  name,
		DisplayName: m.GetBucket().GetDisplayName(),
		Labels:      m.GetBucket().GetLabels(),
	}
	if t := m.GetBucket().GetOwnerTenantId(); t != "" {
		if id, perr := uuid.Parse(t); perr == nil {
			b.OwnerTenantID = id
		}
	}
	out, err := s.H.UpdateBucket(ctx, bucketh.UpdateBucketInput{
		Bucket:          b,
		ExpectedVersion: rv,
		UpdateMask:      m.GetUpdateMask().GetPaths(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(bucketToProto(out)), nil
}

func (s *BucketServer) DeleteBucket(ctx context.Context, req *connect.Request[pb.DeleteBucketRequest]) (*connect.Response[pb.DeleteBucketResponse], error) {
	backend, name, err := bucketNameParts(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseRV(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// Same OCC contract as DeleteTenant: require non-zero rv unless the
	// caller explicitly opts into force-delete via the `delete_on_backend`
	// flag (which already implies "I know what I'm doing, the physical
	// bucket is going too"). Empty rv + delete_on_backend=false → reject.
	if rv == 0 && !req.Msg.GetDeleteOnBackend() {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("resource_version required; pass delete_on_backend=true to bypass"))
	}
	if err := s.H.DeleteBucket(ctx, bucketh.DeleteBucketInput{
		BackendID:       backend,
		BucketName:      name,
		ExpectedVersion: rv,
		DeleteOnBackend: req.Msg.GetDeleteOnBackend(),
	}); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.DeleteBucketResponse{}), nil
}

func (s *BucketServer) SetBucketPolicy(ctx context.Context, req *connect.Request[pb.SetBucketPolicyRequest]) (*connect.Response[pb.Bucket], error) {
	backend, name, err := bucketNameParts(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, _ := parseRV(req.Msg.GetResourceVersion())
	out, err := s.H.SetPolicy(ctx, backend, name, req.Msg.GetCedarPolicy(), rv)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(bucketToProto(out)), nil
}

func (s *BucketServer) SetLifecycleRules(ctx context.Context, req *connect.Request[pb.SetLifecycleRulesRequest]) (*connect.Response[pb.Bucket], error) {
	backend, name, err := bucketNameParts(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, _ := parseRV(req.Msg.GetResourceVersion())
	out, err := s.H.SetLifecycleRules(ctx, backend, name, lifecycleFromProto(req.Msg.GetRules()), rv)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(bucketToProto(out)), nil
}

func (s *BucketServer) SetObjectLock(ctx context.Context, req *connect.Request[pb.SetObjectLockRequest]) (*connect.Response[pb.Bucket], error) {
	backend, name, err := bucketNameParts(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, _ := parseRV(req.Msg.GetResourceVersion())
	out, err := s.H.SetObjectLock(ctx, backend, name, lockFromProto(req.Msg.GetConfig()), rv)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(bucketToProto(out)), nil
}

func (s *BucketServer) SetVersioning(ctx context.Context, req *connect.Request[pb.SetVersioningRequest]) (*connect.Response[pb.Bucket], error) {
	backend, name, err := bucketNameParts(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, _ := parseRV(req.Msg.GetResourceVersion())
	out, err := s.H.SetVersioning(ctx, backend, name, versioningFromProto(req.Msg.GetVersioning()), rv)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(bucketToProto(out)), nil
}

func (s *BucketServer) SetReplication(ctx context.Context, req *connect.Request[pb.SetReplicationRequest]) (*connect.Response[pb.Bucket], error) {
	backend, name, err := bucketNameParts(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, _ := parseRV(req.Msg.GetResourceVersion())
	out, err := s.H.SetReplication(ctx, backend, name, replicationFromProto(req.Msg.GetReplication()), rv)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(bucketToProto(out)), nil
}

var _ paladinadminv1connect.BucketServiceHandler = (*BucketServer)(nil)

// splitBucketCursor decodes "backend/name" form used by ListBuckets pagination.
func splitBucketCursor(tok string) (string, string) {
	for i := 0; i < len(tok); i++ {
		if tok[i] == '/' {
			return tok[:i], tok[i+1:]
		}
	}
	return tok, ""
}
