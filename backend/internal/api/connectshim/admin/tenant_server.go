package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/tenant"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
)

// TenantServer wraps the existing v1 tenant.Handler under the new admin proto.
type TenantServer struct {
	paladinadminv1connect.UnimplementedTenantServiceHandler
	H tenantHandler
}

func NewTenantServer(h *tenant.Handler) *TenantServer { return &TenantServer{H: h} }

func (s *TenantServer) CreateTenant(ctx context.Context, req *connect.Request[pb.CreateTenantRequest]) (*connect.Response[pb.Tenant], error) {
	if err := requireCompilablePolicy(req.Msg.GetTenant().GetInheritedCedarPolicy()); err != nil {
		return nil, err
	}
	args, err := parseCreateTenantArgs(req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	t, err := s.H.CreateTenant(ctx, args)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(tenantDomainToProto(t)), nil
}

// parseCreateTenantArgs is the pure proto→domain translator. Extracted
// so field_mapping_test.go can pin every field's mapping without
// spinning up auth/Cedar/DB. Any new field on CreateTenantRequest or
// its nested Tenant message that needs to reach the handler MUST land
// here — the field-mapping test fails until it does.
func parseCreateTenantArgs(m *pb.CreateTenantRequest) (tenant.CreateTenantArgs, error) {
	src := m.GetTenant()
	args := tenant.CreateTenantArgs{
		Slug:                 src.GetSlug(),
		DisplayName:          src.GetDisplayName(),
		InheritedCedarPolicy: src.GetInheritedCedarPolicy(),
		StorageLayout:        src.GetStorageLayout(),
	}
	if id := m.GetTenantId(); id != "" {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return args, fmt.Errorf("tenant_id: %w", err)
		}
		args.TenantID = parsed
	}
	if labels := src.GetLabels(); len(labels) > 0 {
		b, _ := json.Marshal(labels)
		args.Labels = b
	}
	if name := m.GetDefaultBucket(); name != "" {
		backend, bucket, err := bucketNameParts(name)
		if err != nil {
			return args, fmt.Errorf("default_bucket: %w", err)
		}
		args.DefaultBackendID = backend
		args.DefaultBucketName = bucket
	}
	return args, nil
}

func (s *TenantServer) GetTenant(ctx context.Context, req *connect.Request[pb.GetTenantRequest]) (*connect.Response[pb.Tenant], error) {
	// Resource name format is `tenants/{tenant_id_or_slug}` — accept
	// either form. apiutil.ParseTenantNameRef returns a TenantRef
	// carrying exactly one of {ID, Slug}; we route to the matching
	// handler entry-point.
	ref, err := apiutil.ParseTenantNameRef(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	var t *tenant.Tenant
	if ref.HasID() {
		t, err = s.H.GetTenant(ctx, ref.ID)
	} else {
		t, err = s.H.GetTenantBySlug(ctx, ref.Slug)
	}
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(tenantDomainToProto(t)), nil
}

func storageMigrationToProto(m *tenant.StorageMigration) *pb.StorageMigrationStatus {
	return &pb.StorageMigrationStatus{
		Tenant:        "tenants/" + m.TenantID.String(),
		State:         m.State,
		ObjectsTotal:  m.ObjectsTotal,
		ObjectsCopied: m.ObjectsCopied,
		SourceBucket:  "storageBackends/" + m.SourceBackendName + "/buckets/" + m.SourceBucketName,
		TargetBucket:  "storageBackends/" + m.TargetBackendName + "/buckets/" + m.TargetBucketName,
		Error:         m.Error,
	}
}

func (s *TenantServer) MigrateTenantStorageLayout(ctx context.Context, req *connect.Request[pb.MigrateTenantStorageLayoutRequest]) (*connect.Response[pb.StorageMigrationStatus], error) {
	id, err := s.resolveTenantID(ctx, req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	m, err := s.H.MigrateTenantStorageLayout(ctx, id, req.Msg.GetTargetBackendId(), req.Msg.GetCleanupRetentionSeconds())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(storageMigrationToProto(m)), nil
}

func (s *TenantServer) GetTenantStorageMigration(ctx context.Context, req *connect.Request[pb.GetTenantStorageMigrationRequest]) (*connect.Response[pb.StorageMigrationStatus], error) {
	id, err := s.resolveTenantID(ctx, req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	m, err := s.H.GetTenantStorageMigration(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(storageMigrationToProto(m)), nil
}

func (s *TenantServer) UpdateTenant(ctx context.Context, req *connect.Request[pb.UpdateTenantRequest]) (*connect.Response[pb.Tenant], error) {
	if err := requireCompilablePolicy(req.Msg.GetTenant().GetInheritedCedarPolicy()); err != nil {
		return nil, err
	}
	m := req.Msg
	idStr, err := tenantIDFromName(m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := convx.ParseRV(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
	args := tenant.UpdateTenantArgs{TenantID: id, ExpectedVersion: rv}
	mask := m.GetUpdateMask().GetPaths()
	// Reject attempts to mutate immutable fields. Migration 033 also
	// enforces this at the DB level via a trigger, but catching it
	// here gives a clearer error and avoids burning a tx.
	for _, path := range mask {
		switch path {
		case "tenant_id":
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("tenant_id is immutable"))
		case "slug":
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("slug is immutable; use RenameTenantSlug"))
		}
	}
	src := m.GetTenant()
	if slices.Contains(mask, "display_name") {
		v := src.GetDisplayName()
		args.DisplayName = &v
	}
	if slices.Contains(mask, "labels") {
		b, _ := json.Marshal(src.GetLabels())
		args.Labels = b
	}
	if slices.Contains(mask, "inherited_cedar_policy") {
		v := src.GetInheritedCedarPolicy()
		args.InheritedCedarPolicy = &v
	}
	t, err := s.H.UpdateTenant(ctx, args)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(tenantDomainToProto(t)), nil
}

func (s *TenantServer) DeleteTenant(ctx context.Context, req *connect.Request[pb.DeleteTenantRequest]) (*connect.Response[pb.DeleteTenantResponse], error) {
	idStr, err := tenantIDFromName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := convx.ParseRV(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
	// The OCC guard is required. A race-delete is otherwise silent: caller A
	// reads version 7, caller B deletes without a version, and A's next
	// mutation returns 404 with no signal that the row was concurrently
	// removed.
	if rv == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("resource_version is required"))
	}
	// Always the trash. Hard deletion is PurgeTenant, which is a separate
	// call because it is a separate decision.
	if err := s.H.DeleteTenant(ctx, id, rv); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.DeleteTenantResponse{}), nil
}

// RestoreTenant — soft-delete recovery. Operator-facing — see proto
// commentary for the failure modes (ALREADY_EXISTS on slug collision,
// FAILED_PRECONDITION on already-active rows).
func (s *TenantServer) RestoreTenant(ctx context.Context, req *connect.Request[pb.RestoreTenantRequest]) (*connect.Response[pb.Tenant], error) {
	ref, err := apiutil.ParseTenantNameRef(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// Restore needs a UUID — slug-form lookup would require a list
	// query against trashed rows. Resolve via GetTenantBySlug when
	// slug-form is passed; the handler authz layer rejects if the
	// caller isn't allowed to even see the row.
	tid := ref.ID
	if !ref.HasID() {
		t, err := s.H.GetTenantBySlug(ctx, ref.Slug)
		if err != nil {
			return nil, err
		}
		tid = t.TenantID
	}
	t, err := s.H.RestoreTenant(ctx, tid)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(tenantDomainToProto(t)), nil
}

// PurgeTenant — hard-delete on a trashed row. Refuses to operate on
// an active tenant.
func (s *TenantServer) PurgeTenant(ctx context.Context, req *connect.Request[pb.PurgeTenantRequest]) (*connect.Response[pb.PurgeTenantResponse], error) {
	ref, err := apiutil.ParseTenantNameRef(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	tid := ref.ID
	if !ref.HasID() {
		t, err := s.H.GetTenantBySlug(ctx, ref.Slug)
		if err != nil {
			return nil, err
		}
		tid = t.TenantID
	}
	if err := s.H.PurgeTenant(ctx, tid); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.PurgeTenantResponse{}), nil
}

func (s *TenantServer) ListTenants(ctx context.Context, req *connect.Request[pb.ListTenantsRequest]) (*connect.Response[pb.ListTenantsResponse], error) {
	m := req.Msg
	args := tenant.ListTenantsArgs{
		PageSize:       m.GetPage().GetPageSize(),
		IncludeTrashed: m.GetIncludeTrashed(),
		OnlyTrashed:    m.GetOnlyTrashed(),
		Filter:         m.GetFilter(),
	}
	list, next, err := s.H.ListTenants(ctx, args, m.GetPage().GetPageToken())
	if err != nil {
		return nil, err
	}
	out := &pb.ListTenantsResponse{Page: convx.PageResponseProto(next)}
	for i := range list {
		out.Tenants = append(out.Tenants, tenantDomainToProto(&list[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *TenantServer) SetInheritedPolicy(ctx context.Context, req *connect.Request[pb.SetInheritedPolicyRequest]) (*connect.Response[pb.Tenant], error) {
	m := req.Msg
	idStr, err := tenantIDFromName(m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := convx.ParseRV(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
	policy := m.GetCedarPolicy()
	if err := requireCompilablePolicy(policy); err != nil {
		return nil, err
	}
	t, err := s.H.UpdateTenant(ctx, tenant.UpdateTenantArgs{
		TenantID:             id,
		ExpectedVersion:      rv,
		InheritedCedarPolicy: &policy,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(tenantDomainToProto(t)), nil
}

// RenameTenantSlug rotates the tenant's slug and rewrites every
// `Tenant::"<old_slug>"` reference in inherited + per-collection
// policies. See proto comments and tenant.Handler.RenameTenantSlug.
func (s *TenantServer) RenameTenantSlug(ctx context.Context, req *connect.Request[pb.RenameTenantSlugRequest]) (*connect.Response[pb.Tenant], error) {
	idStr, err := tenantIDFromName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := convx.ParseRV(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
	t, err := s.H.RenameTenantSlug(ctx, tenant.RenameTenantSlugArgs{
		TenantID:        id,
		NewSlug:         req.Msg.GetNewSlug(),
		ExpectedVersion: rv,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(tenantDomainToProto(t)), nil
}

func (s *TenantServer) ResolveRenamedSlug(ctx context.Context, req *connect.Request[pb.ResolveRenamedSlugRequest]) (*connect.Response[pb.ResolveRenamedSlugResponse], error) {
	newSlug, renamedAt, err := s.H.ResolveRenamedSlug(ctx, req.Msg.GetOldSlug())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.ResolveRenamedSlugResponse{
		NewSlug:   newSlug,
		RenamedAt: timestamppb.New(renamedAt),
	}), nil
}

var _ paladinadminv1connect.TenantServiceHandler = (*TenantServer)(nil)

func tenantDomainToProto(t *tenant.Tenant) *pb.Tenant {
	if t == nil {
		return nil
	}
	out := &pb.Tenant{
		Name:     fmt.Sprintf("tenants/%s", t.TenantID),
		TenantId: t.TenantID.String(),
		// Domain Tenant.Slug is populated by Repository reads
		// (see migrations/001_initial_schema.sql + handler.CreateTenant
		// fallback). Front-end uses it to render slug-first URLs and
		// canonicalise UUID URLs to it; empty slug is impossible
		// after the schema baseline (001_initial_schema.sql) backfilled every row, but we surface
		// whatever's there rather than synthesise.
		Slug:                 t.Slug,
		DisplayName:          t.DisplayName,
		InheritedCedarPolicy: t.InheritedCedarPolicy,
		ResourceVersion:      convx.ResourceVersion(t.ResourceVersion),
		CreatedAt:            convx.TsProto(t.CreatedAt),
		UpdatedAt:            convx.TsProto(t.UpdatedAt),
		DefaultBucket:        t.DefaultBucket,
		StorageLayout:        t.StorageLayout,
	}
	if !t.DeletedAt.IsZero() {
		out.DeletedAt = convx.TsProto(t.DeletedAt)
	}
	if len(t.Labels) > 0 {
		var m map[string]string
		if err := json.Unmarshal(t.Labels, &m); err == nil {
			out.Labels = m
		}
	}
	return out
}

// ─── Tenant default binding (ADR-0014 Phase 3) ──────────────────────────────

// resolveTenantID maps a "tenants/{id_or_slug}" name to a tenant UUID, using
// GetTenantBySlug for the slug form (its authz layer gates visibility).
func (s *TenantServer) resolveTenantID(ctx context.Context, name string) (uuid.UUID, error) {
	ref, err := apiutil.ParseTenantNameRef(name)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if ref.HasID() {
		return ref.ID, nil
	}
	t, err := s.H.GetTenantBySlug(ctx, ref.Slug)
	if err != nil {
		return uuid.Nil, err
	}
	return t.TenantID, nil
}

func (s *TenantServer) GetTenantDefaultBinding(ctx context.Context, req *connect.Request[pb.GetTenantDefaultBindingRequest]) (*connect.Response[pb.TenantDefaultBinding], error) {
	tid, err := s.resolveTenantID(ctx, req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	b, err := s.H.GetDefaultBinding(ctx, tid)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(defaultBindingToProto(b)), nil
}

func (s *TenantServer) SetTenantDefaultBinding(ctx context.Context, req *connect.Request[pb.SetTenantDefaultBindingRequest]) (*connect.Response[pb.TenantDefaultBinding], error) {
	tid, err := s.resolveTenantID(ctx, req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	b, err := s.H.SetDefaultBinding(ctx, tid, req.Msg.GetBucket())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(defaultBindingToProto(b)), nil
}

func (s *TenantServer) ClearTenantDefaultBinding(ctx context.Context, req *connect.Request[pb.ClearTenantDefaultBindingRequest]) (*connect.Response[pb.ClearTenantDefaultBindingResponse], error) {
	tid, err := s.resolveTenantID(ctx, req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	if err := s.H.ClearDefaultBinding(ctx, tid); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.ClearTenantDefaultBindingResponse{}), nil
}

func defaultBindingToProto(b *tenant.DefaultBinding) *pb.TenantDefaultBinding {
	return &pb.TenantDefaultBinding{
		Name: "tenants/" + b.TenantID.String() + "/defaultBinding",
		// One reference, one field: "storageBackends/{backend}/buckets/{bucket}".
		Bucket: "storageBackends/" + b.BackendName + "/buckets/" + b.BucketName,
		SetAt:  timestamppb.New(b.SetAt),
		SetBy:  b.SetBy,
	}
}
