package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"

	objectkey "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/collectionh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/resolve"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/publicread"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
)

// defaultBindingSource resolves a tenant's default (backend, bucket) route so
// CreateCollection can route a NEW collection when the caller omits the bucket
// (ADR-0014 Phase 3). Satisfied by tenant.Repository.
type defaultBindingSource interface {
	GetDefaultBinding(ctx context.Context, tenantID uuid.UUID) (tenanth.DefaultBinding, error)
}

// tenantSlugSource resolves a tenant slug to its row, authorising the caller
// on the way. Satisfied by tenanth.Handler.
type tenantSlugSource interface {
	GetTenantBySlug(ctx context.Context, slug string) (*tenanth.Tenant, error)
}

type CollectionServer struct {
	paladinadminv1connect.UnimplementedCollectionServiceHandler
	H        collectionHandler
	bindings defaultBindingSource
	tenants  tenantSlugSource
}

func NewCollectionServer(h *objectkey.Handler, bindings defaultBindingSource, tenants tenantSlugSource) *CollectionServer {
	return &CollectionServer{H: h, bindings: bindings, tenants: tenants}
}

// tenantParent resolves a `tenants/{tenant_id_or_slug}` parent, as the proto
// documents it, to a tenant id; empty means the caller's whole scope. A trailing
// "/…" after the tenant segment is tolerated. ListCollections used to parse the
// parent as a UUID and drop the filter when that failed, so a slug listed
// across every tenant — which for most callers is an empty page, and read as
// "this tenant has no collections".
func (s *CollectionServer) tenantParent(ctx context.Context, parent string) (uuid.UUID, error) {
	if parent == "" {
		return uuid.Nil(), nil
	}
	tenant, _, _ := strings.Cut(strings.TrimPrefix(parent, apiutil.TenantNamePrefix), "/")
	ref, err := apiutil.ParseTenantNameRef(tenant)
	if err != nil {
		return uuid.Nil(), rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid tenant parent %q: %w", parent, err))
	}
	if ref.HasID() {
		return ref.ID, nil
	}
	t, err := s.tenants.GetTenantBySlug(ctx, ref.Slug)
	if err != nil {
		return uuid.Nil(), err
	}
	return t.TenantID, nil
}

// Log field keys and the operation name of the refusals CreateCollection
// answers itself; the handler logs its own under the same keys.
const (
	logOp              = "op"
	logCollection      = "collection"
	logParent          = "parent"
	logCode            = "code"
	opCreateCollection = "CollectionService.CreateCollection"
)

// errNoDefaultBinding answers a create that names no bucket for a tenant with
// no default binding to route it to.
var errNoDefaultBinding = errors.New(
	"no bucket specified and the tenant has no default binding; set one via SetTenantDefaultBinding or name a bucket")

func (s *CollectionServer) CreateCollection(ctx context.Context, req *pb.CreateCollectionRequest) (*pb.Collection, error) {
	if err := requireCompilablePolicy(req.GetCollectionResource().GetCedarPolicy()); err != nil {
		return nil, err
	}
	// The request is judged before anything is read for it: a malformed name
	// or bucket is the caller's mistake whatever the tenant's state, and a
	// missing default binding answered first hid it until the binding was set.
	backend, bucket, err := admitCreateRequest(req)
	if err != nil {
		return nil, refuseCreate(ctx, req, err)
	}
	tenantID, err := s.tenantParent(ctx, req.GetParent())
	if err != nil {
		return nil, err
	}
	src := req.GetCollectionResource()
	// Bare-name ergonomics (ADR-0014 Phase 3): if the caller creates an
	// collection without naming a bucket, route it to the tenant's default
	// binding. This is the CREATION case only — an existing collection keeps its
	// own (backend, bucket), which the Get/Update/Delete paths resolve from the
	// row, never from the tenant default.
	if bucket == "" {
		db, err := s.bindings.GetDefaultBinding(ctx, tenantID)
		if err != nil {
			if errors.Is(err, tenanth.ErrNotFound) {
				return nil, refuseCreate(ctx, req, connect.NewError(connect.CodeFailedPrecondition,
					errNoDefaultBinding.Error()).WithCause(errNoDefaultBinding))
			}
			return nil, err
		}
		backend, bucket = db.BackendName, db.BucketName
	}
	args := objectkey.CreateCollectionArgs{
		TenantID:     tenantID,
		Collection:   req.GetCollection(),
		DisplayName:  src.GetDisplayName(),
		BackendID:    backend,
		BucketName:   bucket,
		CedarPolicy:  src.GetCedarPolicy(),
		PublicRead:   src.GetAccess() == pb.CollectionAccess_COLLECTION_ACCESS_PUBLIC_READ,
		CacheControl: src.GetCacheControl(),
	}
	out, err := s.H.CreateCollection(ctx, args)
	if err != nil {
		return nil, err
	}
	return collectionDomainToProto(out), nil
}

// admitCreateRequest refuses a create whose own fields are wrong, reading no
// state: a collection outside the grammar, a bucket that is not a bucket
// resource name, or a public collection that names no bucket. It returns the
// named bucket, empty when the request names none.
func admitCreateRequest(req *pb.CreateCollectionRequest) (backend, bucket string, err error) {
	if err := objectkey.AdmitCollectionName(req.GetCollection()); err != nil {
		return "", "", err
	}
	src := req.GetCollectionResource()
	backend, bucket, err = bucketRef(src.GetBucket())
	if err != nil {
		return "", "", rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("bucket: %w", err))
	}
	// A tenant's default binding is a private bucket; a public collection
	// has to say which public bucket it lives in (ADR-0027).
	if bucket == "" && src.GetAccess() == pb.CollectionAccess_COLLECTION_ACCESS_PUBLIC_READ {
		return "", "", publicread.Rulef("a public collection must name its public bucket")
	}
	return backend, bucket, nil
}

// refuseCreate logs a refusal CreateCollection answers before the handler
// runs, at Warn, and returns it mapped for the caller.
func refuseCreate(ctx context.Context, req *pb.CreateCollectionRequest, err error) error {
	mapped := apiutil.MapError(err)
	logger.FromContext(ctx).Warn("collection operation refused",
		zap.String(logOp, opCreateCollection),
		zap.String(logParent, req.GetParent()),
		zap.String(logCollection, req.GetCollection()),
		zap.String(logCode, connect.CodeOf(mapped).String()),
		zap.Error(err))
	return mapped
}

func (s *CollectionServer) GetCollection(ctx context.Context, req *pb.GetCollectionRequest) (*pb.Collection, error) {
	ref, err := resolve.ResolveCollectionName(ctx, req.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	out, err := s.H.GetCollection(ctx, ref.TenantID, ref.Collection)
	if err != nil {
		return nil, err
	}
	return collectionDomainToProto(out), nil
}

// updateCollectionPaths are the Collection fields UpdateCollection applies.
var updateCollectionPaths = []string{"display_name", "cedar_policy"}

func (s *CollectionServer) UpdateCollection(ctx context.Context, req *pb.UpdateCollectionRequest) (*pb.Collection, error) {
	if err := requireCompilablePolicy(req.GetCollectionResource().GetCedarPolicy()); err != nil {
		return nil, err
	}
	m := req
	ref, err := resolve.ResolveCollectionName(ctx, m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	rv, err := convx.ParseRV(m.GetResourceVersion())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err))
	}
	src := m.GetCollectionResource()
	args := objectkey.UpdateCollectionArgs{
		TenantID:        ref.TenantID,
		Collection:      ref.Collection,
		ExpectedVersion: rv,
	}
	mask := m.GetUpdateMask().GetPaths()
	if err := convx.CheckMask(mask, updateCollectionPaths); err != nil {
		return nil, err
	}
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
	return collectionDomainToProto(out), nil
}

func (s *CollectionServer) DeleteCollection(ctx context.Context, req *pb.DeleteCollectionRequest) (*pb.DeleteCollectionResponse, error) {
	ref, err := resolve.ResolveCollectionName(ctx, req.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	rv, err := convx.ParseRV(req.GetResourceVersion())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err))
	}
	// Same OCC contract as DeleteTenant / DeleteBackend: require a guard
	// unless the caller explicitly opts out. Without this the request had a
	// force flag but no guard to force past — an omitted resource_version
	// simply skipped the check (expected_version=0 disables it in SQL).
	if rv == 0 && !req.GetSkipVersionCheck() {
		return nil, connect.Errorf(connect.CodeInvalidArgument,
			"resource_version is required; pass force=true to bypass")
	}
	// ref.TenantID, not the caller's: a C-shape name says which tenant's
	// collection this is, and Get and List have always read it. Delete used to
	// drop it on the floor and operate on the caller's own tenant instead.
	if err := s.H.DeleteCollection(ctx, ref.TenantID, ref.Collection, rv); err != nil {
		return nil, err
	}
	return &pb.DeleteCollectionResponse{}, nil
}

func (s *CollectionServer) ListCollections(ctx context.Context, req *pb.ListCollectionsRequest) (*pb.ListCollectionsResponse, error) {
	m := req
	args := objectkey.ListCollectionsArgs{
		PageSize:  m.GetPage().GetPageSize(),
		PageToken: m.GetPage().GetPageToken(),
		Filter:    m.GetFilter(),
	}
	tenantID, err := s.tenantParent(ctx, m.GetParent())
	if err != nil {
		return nil, err
	}
	args.TenantID = tenantID
	if b := m.GetBucket(); b != "" {
		backend, bucket, err := bucketRef(b)
		if err != nil {
			return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("bucket: %w", err))
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
	return out, nil
}

func (s *CollectionServer) SetCollectionPolicy(ctx context.Context, req *pb.SetCollectionPolicyRequest) (*pb.Collection, error) {
	ref, err := resolve.ResolveCollectionName(ctx, req.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	rv, err := convx.ParseRV(req.GetResourceVersion())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err))
	}
	policy := req.GetCedarPolicy()
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
	return collectionDomainToProto(out), nil
}

func (s *CollectionServer) BindCollectionToBucket(ctx context.Context, req *pb.BindCollectionToBucketRequest) (*pb.Collection, error) {
	m := req
	ref, err := resolve.ResolveCollectionName(ctx, m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	rv, err := convx.ParseRV(m.GetResourceVersion())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err))
	}
	out, err := s.H.BindCollectionToBucket(ctx, ref.TenantID, ref.Collection, m.GetBucket(), rv)
	if err != nil {
		return nil, err
	}
	return collectionDomainToProto(out), nil
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
		Access:          collectionAccessToProto(o.PublicRead),
		CacheControl:    o.CacheControl,
		Usage:           collectionUsageToProto(o.Usage),
	}
}

func collectionUsageToProto(u objectkey.CollectionUsage) *pb.CollectionUsage {
	out := &pb.CollectionUsage{ObjectCount: u.ObjectCount, TotalBytes: u.TotalBytes}
	if u.LastCommittedAt != nil {
		out.LastCommittedAt = convx.TsProto(*u.LastCommittedAt)
	}
	return out
}

func collectionAccessToProto(publicRead bool) pb.CollectionAccess {
	if publicRead {
		return pb.CollectionAccess_COLLECTION_ACCESS_PUBLIC_READ
	}
	return pb.CollectionAccess_COLLECTION_ACCESS_PRIVATE
}

// silence unused imports warning if needed
var _ = json.Marshal
