package admin

import (
	"context"
	"fmt"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"

	"connectrpc.com/connect/v2"
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

func (s *BucketServer) CreateBucket(ctx context.Context, req *pb.CreateBucketRequest) (*pb.Bucket, error) {
	if err := requireCompilablePolicy(req.GetBucket().GetCedarPolicy()); err != nil {
		return nil, err
	}
	m := req
	backend, err := backendIDFromName(m.GetParent())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
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
		// ADR-0027: fixed here, at creation, and never again.
		PublicRead:    src.GetPublicRead(),
		PublicBaseURL: src.GetPublicBaseUrl(),
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
	return bucketToProto(out), nil
}

func (s *BucketServer) GetBucket(ctx context.Context, req *pb.GetBucketRequest) (*pb.Bucket, error) {
	backend, name, err := bucketNameParts(req.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	out, err := s.H.GetBucket(ctx, backend, name)
	if err != nil {
		return nil, err
	}
	return bucketToProto(out), nil
}

func (s *BucketServer) ListBuckets(ctx context.Context, req *pb.ListBucketsRequest) (*pb.ListBucketsResponse, error) {
	m := req
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
			return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("owner_tenant_id must be a UUID: %w", err))
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
	return out, nil
}

func (s *BucketServer) ListAccessibleBuckets(ctx context.Context, req *pb.ListAccessibleBucketsRequest) (*pb.ListBucketsResponse, error) {
	m := req
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
	return out, nil
}

// updateBucketPaths are the Bucket fields UpdateBucket applies.
var updateBucketPaths = []string{"display_name", "labels", "owner_tenant_id"}

func (s *BucketServer) UpdateBucket(ctx context.Context, req *pb.UpdateBucketRequest) (*pb.Bucket, error) {
	if err := requireCompilablePolicy(req.GetBucket().GetCedarPolicy()); err != nil {
		return nil, err
	}
	m := req
	backend, name, err := bucketNameParts(m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	rv, err := convx.ParseRV(m.GetResourceVersion())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err))
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
	return bucketToProto(out), nil
}

func (s *BucketServer) DeleteBucket(ctx context.Context, req *pb.DeleteBucketRequest) (*pb.DeleteBucketResponse, error) {
	backend, name, err := bucketNameParts(req.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	rv, err := convx.ParseRV(req.GetResourceVersion())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err))
	}
	// Same OCC contract as DeleteTenant / DeleteBackend: require a guard
	// unless the caller explicitly opts out via `force`.
	//
	// The opt-out used to be `delete_on_backend`, which had the risk gradient
	// backwards — the one form of this call that also erases the physical
	// bucket was the only one exempt from the concurrency check. Whether the
	// physical bucket goes and whether the caller holds a current version are
	// independent decisions.
	if rv == 0 && !req.GetSkipVersionCheck() {
		return nil, connect.Errorf(connect.CodeInvalidArgument,
			"resource_version is required; pass skip_version_check=true to bypass")
	}
	if err := s.H.DeleteBucket(ctx, bucketh.DeleteBucketInput{
		BackendID:       backend,
		BucketName:      name,
		ExpectedVersion: rv,
		DeleteOnBackend: req.GetDeleteOnBackend(),
	}); err != nil {
		return nil, err
	}
	return &pb.DeleteBucketResponse{}, nil
}

func (s *BucketServer) SetBucketPolicy(ctx context.Context, req *pb.SetBucketPolicyRequest) (*pb.Bucket, error) {
	if err := requireCompilablePolicy(req.GetCedarPolicy()); err != nil {
		return nil, err
	}
	backend, name, err := bucketNameParts(req.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	rv, err := convx.ParseRV(req.GetResourceVersion())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err))
	}
	out, err := s.H.SetPolicy(ctx, backend, name, req.GetCedarPolicy(), rv)
	if err != nil {
		return nil, err
	}
	return bucketToProto(out), nil
}

func (s *BucketServer) SetLifecycleRules(ctx context.Context, req *pb.SetLifecycleRulesRequest) (*pb.Bucket, error) {
	backend, name, err := bucketNameParts(req.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	rv, err := convx.ParseRV(req.GetResourceVersion())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err))
	}
	out, err := s.H.SetLifecycleRules(ctx, backend, name, lifecycleFromProto(req.GetRules()), rv)
	if err != nil {
		return nil, err
	}
	return bucketToProto(out), nil
}

func (s *BucketServer) SetObjectLock(ctx context.Context, req *pb.SetObjectLockRequest) (*pb.Bucket, error) {
	backend, name, err := bucketNameParts(req.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	rv, err := convx.ParseRV(req.GetResourceVersion())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err))
	}
	out, err := s.H.SetObjectLock(ctx, backend, name, lockFromProto(req.GetConfig()), rv)
	if err != nil {
		return nil, err
	}
	return bucketToProto(out), nil
}

func (s *BucketServer) SetVersioning(ctx context.Context, req *pb.SetVersioningRequest) (*pb.Bucket, error) {
	backend, name, err := bucketNameParts(req.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	rv, err := convx.ParseRV(req.GetResourceVersion())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err))
	}
	out, err := s.H.SetVersioning(ctx, backend, name, versioningFromProto(req.GetVersioning()), rv)
	if err != nil {
		return nil, err
	}
	return bucketToProto(out), nil
}

func (s *BucketServer) SetReplication(ctx context.Context, req *pb.SetReplicationRequest) (*pb.Bucket, error) {
	backend, name, err := bucketNameParts(req.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	rv, err := convx.ParseRV(req.GetResourceVersion())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err))
	}
	out, err := s.H.SetReplication(ctx, backend, name, replicationFromProto(req.GetReplication()), rv)
	if err != nil {
		return nil, err
	}
	return bucketToProto(out), nil
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
