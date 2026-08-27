package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/oleg-tkachuk/paladin/internal/api/connectshim/convx"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/connectshim/resolve"
	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1/paladinadminv1connect"
	objectkey "github.com/oleg-tkachuk/paladin/internal/api/v1/collection"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
)

// defaultBindingSource resolves a tenant's default (backend, bucket) route so
// CreateCollection can route a NEW collection when the caller omits the bucket
// (ADR-0010 Phase 3). Satisfied by tenant.Repository.
type defaultBindingSource interface {
	GetDefaultBinding(ctx context.Context, tenantID uuid.UUID) (tenant.DefaultBinding, error)
}

type CollectionServer struct {
	paladinadminv1connect.UnimplementedCollectionServiceHandler
	H        *objectkey.Handler
	bindings defaultBindingSource
}

func NewCollectionServer(h *objectkey.Handler, bindings defaultBindingSource) *CollectionServer {
	return &CollectionServer{H: h, bindings: bindings}
}

func (s *CollectionServer) CreateCollection(ctx context.Context, req *connect.Request[pb.CreateCollectionRequest]) (*connect.Response[pb.Collection], error) {
	if err := requireCompilablePolicy(req.Msg.GetCollectionResource().GetCedarPolicy()); err != nil {
		return nil, err
	}
	m := req.Msg
	tenantID, err := resolve.ResolveTenantParent(m.GetParent())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	src := m.GetCollectionResource()
	backend, bucket, _ := bucketRef(src.GetBucket())
	// Bare-name ergonomics (ADR-0010 Phase 3): if the caller creates an
	// collection without naming a bucket, route it to the tenant's default
	// binding. This is the CREATION case only — an existing collection keeps its
	// own (backend, bucket), which the Get/Update/Delete paths resolve from the
	// row, never from the tenant default.
	if bucket == "" {
		db, err := s.bindings.GetDefaultBinding(ctx, tenantID)
		if err != nil {
			if errors.Is(err, tenant.ErrNotFound) {
				return nil, connect.NewError(connect.CodeFailedPrecondition,
					errors.New("no bucket specified and the tenant has no default binding; set one via SetTenantDefaultBinding or name a bucket"))
			}
			return nil, err
		}
		backend, bucket = db.BackendName, db.BucketName
	}
	args := objectkey.CreateCollectionArgs{
		TenantID:    tenantID,
		Collection:  m.GetCollection(),
		DisplayName: src.GetDisplayName(),
		BackendID:   backend,
		BucketName:  bucket,
		CedarPolicy: src.GetCedarPolicy(),
	}
	out, err := s.H.CreateCollection(ctx, args)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(collectionDomainToProto(out)), nil
}

func (s *CollectionServer) GetCollection(ctx context.Context, req *connect.Request[pb.GetCollectionRequest]) (*connect.Response[pb.Collection], error) {
	ref, err := resolve.ResolveCollectionName(ctx, req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.GetCollection(ctx, ref.TenantID, ref.Collection)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(collectionDomainToProto(out)), nil
}

func (s *CollectionServer) UpdateCollection(ctx context.Context, req *connect.Request[pb.UpdateCollectionRequest]) (*connect.Response[pb.Collection], error) {
	if err := requireCompilablePolicy(req.Msg.GetCollectionResource().GetCedarPolicy()); err != nil {
		return nil, err
	}
	m := req.Msg
	ref, err := resolve.ResolveCollectionName(ctx, m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := convx.ParseRV(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
	src := m.GetCollectionResource()
	args := objectkey.UpdateCollectionArgs{
		TenantID:        ref.TenantID,
		Collection:      ref.Collection,
		ExpectedVersion: rv,
	}
	mask := m.GetUpdateMask().GetPaths()
	if slices.Contains(mask, "display_name") {
		v := src.GetDisplayName()
		args.DisplayName = &v
	}
	if slices.Contains(mask, "cedar_policy") {
		v := src.GetCedarPolicy()
		args.CedarPolicy = &v
	}
	out, err := s.H.UpdateCollection(ctx, args)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(collectionDomainToProto(out)), nil
}

func (s *CollectionServer) DeleteCollection(ctx context.Context, req *connect.Request[pb.DeleteCollectionRequest]) (*connect.Response[pb.DeleteCollectionResponse], error) {
	ref, err := resolve.ResolveCollectionName(ctx, req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := convx.ParseRV(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
	// Same OCC contract as DeleteTenant / DeleteBackend: require a guard
	// unless the caller explicitly opts out. Without this the request had a
	// force flag but no guard to force past — an omitted resource_version
	// simply skipped the check (expected_version=0 disables it in SQL).
	if rv == 0 && !req.Msg.GetSkipVersionCheck() {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("resource_version is required; pass force=true to bypass"))
	}
	// ref.TenantID, not the caller's: a C-shape name says which tenant's
	// collection this is, and Get and List have always read it. Delete used to
	// drop it on the floor and operate on the caller's own tenant instead.
	if err := s.H.DeleteCollection(ctx, ref.TenantID, ref.Collection, rv); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.DeleteCollectionResponse{}), nil
}

func (s *CollectionServer) ListCollections(ctx context.Context, req *connect.Request[pb.ListCollectionsRequest]) (*connect.Response[pb.ListCollectionsResponse], error) {
	m := req.Msg
	args := objectkey.ListCollectionsArgs{
		PageSize:  m.GetPage().GetPageSize(),
		PageToken: m.GetPage().GetPageToken(),
		Filter:    m.GetFilter(),
	}
	if m.GetParent() != "" {
		if id, err := resolve.ResolveTenantParent(m.GetParent()); err == nil {
			args.TenantID = id
		}
	}
	if b := m.GetBucket(); b != "" {
		backend, bucket, err := bucketRef(b)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("bucket: %w", err))
		}
		args.BackendID = backend
		args.BucketName = bucket
	}
	list, next, err := s.H.ListCollections(ctx, args)
	if err != nil {
		return nil, err
	}
	out := &pb.ListCollectionsResponse{Page: convx.PageResponseProto(next)}
	for i := range list {
		out.Collections = append(out.Collections, collectionDomainToProto(&list[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *CollectionServer) SetCollectionPolicy(ctx context.Context, req *connect.Request[pb.SetCollectionPolicyRequest]) (*connect.Response[pb.Collection], error) {
	ref, err := resolve.ResolveCollectionName(ctx, req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := convx.ParseRV(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
	policy := req.Msg.GetCedarPolicy()
	if err := requireCompilablePolicy(policy); err != nil {
		return nil, err
	}
	out, err := s.H.UpdateCollection(ctx, objectkey.UpdateCollectionArgs{
		TenantID:        ref.TenantID,
		Collection:      ref.Collection,
		ExpectedVersion: rv,
		CedarPolicy:     &policy,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(collectionDomainToProto(out)), nil
}

func (s *CollectionServer) BindCollectionToBucket(ctx context.Context, req *connect.Request[pb.BindCollectionToBucketRequest]) (*connect.Response[pb.Collection], error) {
	m := req.Msg
	ref, err := resolve.ResolveCollectionName(ctx, m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := convx.ParseRV(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
	out, err := s.H.BindCollectionToBucket(ctx, ref.TenantID, ref.Collection, m.GetBucket(), rv)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(collectionDomainToProto(out)), nil
}

var _ paladinadminv1connect.CollectionServiceHandler = (*CollectionServer)(nil)

// bucketRef decodes "storageBackends/{backend}/buckets/{bucket}".
func bucketRef(name string) (backend, bucket string, err error) {
	if name == "" {
		return "", "", nil
	}
	return bucketNameParts(name)
}

func collectionDomainToProto(o *objectkey.Collection) *pb.Collection {
	if o == nil {
		return nil
	}
	return &pb.Collection{
		Name:            fmt.Sprintf("tenants/%s/collections/%s", o.TenantID, o.Collection),
		TenantId:        o.TenantID.String(),
		Collection:      o.Collection,
		DisplayName:     o.DisplayName,
		Bucket:          fmt.Sprintf("storageBackends/%s/buckets/%s", o.BackendID, o.BucketName),
		CedarPolicy:     o.CedarPolicy,
		ResourceVersion: convx.ResourceVersion(o.ResourceVersion),
		CreatedAt:       convx.TsProto(o.CreatedAt),
		UpdatedAt:       convx.TsProto(o.UpdatedAt),
	}
}

// silence unused imports warning if needed
var _ = json.Marshal
