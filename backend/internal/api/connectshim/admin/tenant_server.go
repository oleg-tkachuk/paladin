package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1/paladinadminv1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
)

// TenantServer wraps the existing v1 tenant.Handler under the new admin proto.
type TenantServer struct {
	paladinadminv1connect.UnimplementedTenantServiceHandler
	H *tenant.Handler
}

func NewTenantServer(h *tenant.Handler) *TenantServer { return &TenantServer{H: h} }

func (s *TenantServer) CreateTenant(ctx context.Context, req *connect.Request[pb.CreateTenantRequest]) (*connect.Response[pb.Tenant], error) {
	m := req.Msg
	src := m.GetTenant()
	args := tenant.CreateTenantArgs{
		DisplayName:          src.GetDisplayName(),
		InheritedCedarPolicy: src.GetInheritedCedarPolicy(),
	}
	if id := m.GetTenantId(); id != "" {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		args.TenantID = parsed
	}
	if labels := src.GetLabels(); len(labels) > 0 {
		b, _ := json.Marshal(labels)
		args.Labels = b
	}
	t, err := s.H.CreateTenant(ctx, args)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(tenantDomainToProto(t)), nil
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

func (s *TenantServer) UpdateTenant(ctx context.Context, req *connect.Request[pb.UpdateTenantRequest]) (*connect.Response[pb.Tenant], error) {
	m := req.Msg
	idStr, err := tenantIDFromName(m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseRV(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	args := tenant.UpdateTenantArgs{TenantID: id, ExpectedVersion: rv}
	mask := m.GetUpdateMask().GetPaths()
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
	rv, _ := parseRV(req.Msg.GetResourceVersion())
	if err := s.H.DeleteTenant(ctx, id, rv); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.DeleteTenantResponse{}), nil
}

func (s *TenantServer) ListTenants(ctx context.Context, req *connect.Request[pb.ListTenantsRequest]) (*connect.Response[pb.ListTenantsResponse], error) {
	m := req.Msg
	list, next, err := s.H.ListTenants(ctx, m.GetPage().GetPageSize(), m.GetPage().GetPageToken())
	if err != nil {
		return nil, err
	}
	out := &pb.ListTenantsResponse{Page: pageResponseProto(next)}
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
	rv, _ := parseRV(m.GetResourceVersion())
	policy := m.GetCedarPolicy()
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
// `Tenant::"<old_slug>"` reference in inherited + per-objectKey
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
	rv, err := parseRV(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
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

var _ paladinadminv1connect.TenantServiceHandler = (*TenantServer)(nil)

func tenantDomainToProto(t *tenant.Tenant) *pb.Tenant {
	if t == nil {
		return nil
	}
	out := &pb.Tenant{
		Name:     fmt.Sprintf("tenants/%s", t.TenantID),
		TenantId: t.TenantID.String(),
		// Domain Tenant.Slug is populated by Repository reads
		// (see migrations/009_tenant_slug.sql + handler.CreateTenant
		// fallback). Front-end uses it to render slug-first URLs and
		// canonicalise UUID URLs to it; empty slug is impossible
		// after migration 009 backfilled every row, but we surface
		// whatever's there rather than synthesise.
		Slug:                 t.Slug,
		DisplayName:          t.DisplayName,
		InheritedCedarPolicy: t.InheritedCedarPolicy,
		ResourceVersion:      resourceVersion(t.ResourceVersion),
		CreatedAt:            tsProto(t.CreatedAt),
		UpdatedAt:            tsProto(t.UpdatedAt),
	}
	if len(t.Labels) > 0 {
		var m map[string]string
		if err := json.Unmarshal(t.Labels, &m); err == nil {
			out.Labels = m
		}
	}
	return out
}
