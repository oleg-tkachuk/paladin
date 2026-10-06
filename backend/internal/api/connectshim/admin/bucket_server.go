package admin

import (
	"context"
	"fmt"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/bucketh"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
)

type BucketServer struct {
	paladinadminv1connect.UnimplementedBucketServiceHandler
	H bucketHandler
	// Tenants resolves the slug form of ListAccessibleBuckets' tenant name.
	Tenants TenantResolver
}

func NewBucketServer(h *bucketh.Handler, tenants TenantResolver) *BucketServer {
	return &BucketServer{H: h, Tenants: tenants}
}

func (s *BucketServer) CreateBucket(ctx context.Context, req *connect.Request[pb.CreateBucketRequest]) (*connect.Response[pb.Bucket], error) {
	if err := requireCompilablePolicy(req.Msg.GetBucket().GetCedarPolicy()); err != nil {
		return nil, err
	}
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
		Filter:   m.GetFilter(),
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
	out := &pb.ListBucketsResponse{Page: convx.PageResponseProto(next)}
	for i := range list {
		out.Buckets = append(out.Buckets, bucketToProto(&list[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *BucketServer) ListAccessibleBuckets(ctx context.Context, req *connect.Request[pb.ListAccessibleBucketsRequest]) (*connect.Response[pb.ListBucketsResponse], error) {
	m := req.Msg
	id, err := resolveTenantName(ctx, s.Tenants, m.GetTenant())
	if err != nil {
		return nil, err
	}
	afterBackend, afterName := splitBucketCursor(m.GetPage().GetPageToken())
	list, next, err := s.H.ListAccessibleBuckets(ctx, id, m.GetPage().GetPageSize(), afterBackend, afterName)
	if err != nil {
		return nil, err
	}
	out := &pb.ListBucketsResponse{Page: convx.PageResponseProto(next)}
	for i := range list {
		out.Buckets = append(out.Buckets, bucketToProto(&list[i]))
	}
	return connect.NewResponse(out), nil
}

// updateBucketPaths are the Bucket fields UpdateBucket applies.
var updateBucketPaths = []string{"display_name", "labels", "owner_tenant_id"}

func (s *BucketServer) UpdateBucket(ctx context.Context, req *connect.Request[pb.UpdateBucketRequest]) (*connect.Response[pb.Bucket], error) {
	if err := requireCompilablePolicy(req.Msg.GetBucket().GetCedarPolicy()); err != nil {
		return nil, err
	}
	m := req.Msg
	backend, name, err := bucketNameParts(m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := convx.ParseRV(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
	if err := convx.CheckMask(m.GetUpdateMask().GetPaths(), updateBucketPaths); err != nil {
		return nil, err
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
	rv, err := convx.ParseRV(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
	// Same OCC contract as DeleteTenant / DeleteBackend: require a guard
	// unless the caller explicitly opts out via `force`.
	//
	// The opt-out used to be `delete_on_backend`, which had the risk gradient
	// backwards — the one form of this call that also erases the physical
	// bucket was the only one exempt from the concurrency check. Whether the
	// physical bucket goes and whether the caller holds a current version are
	// independent decisions.
	if rv == 0 && !req.Msg.GetSkipVersionCheck() {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("resource_version is required; pass skip_version_check=true to bypass"))
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
	if err := requireCompilablePolicy(req.Msg.GetCedarPolicy()); err != nil {
		return nil, err
	}
	backend, name, err := bucketNameParts(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := convx.ParseRV(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
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
	rv, err := convx.ParseRV(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
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
	rv, err := convx.ParseRV(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
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
	rv, err := convx.ParseRV(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
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
	rv, err := convx.ParseRV(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
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
