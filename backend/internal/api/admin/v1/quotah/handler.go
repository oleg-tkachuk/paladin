// Package quotah implements the admin QuotaService — set/get usage caps.
package quotah

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

type Handler struct {
	repo   admindomain.QuotaRepository
	policy cedar.Authorizer
}

func NewHandler(r admindomain.QuotaRepository, policy cedar.Authorizer) *Handler {
	if policy == nil {
		panic("quotah: policy authorizer is required")
	}
	return &Handler{repo: r, policy: policy}
}

// authorize gates a quota RPC against Cedar. The Resource carries the
// tenant or bucket coordinates so policies can pin "tenant.admin manages
// own quota" via resource.tenant_id == principal.tenant_id.
func (h *Handler) authorize(ctx context.Context, action string, q admindomain.Quota) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{Subject: p.Subject, TenantID: p.TenantID, TenantSlug: p.TenantSlug, Roles: p.Roles, Scopes: apiutil.ScopeStrings(p.Scopes)},
		action,
		&cedar.Resource{
			TenantID:   q.TenantID,
			BackendID:  q.BackendID,
			BucketName: q.BucketName,
		},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))
	}
	return nil
}

func (h *Handler) GetTenantQuota(ctx context.Context, tenantID uuid.UUID) (*admindomain.Quota, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) && tenantID != caller {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("cross-tenant denied"))
	}
	if err := h.authorize(ctx, cedar.ActionReadQuota, admindomain.Quota{TenantID: tenantID}); err != nil {
		return nil, err
	}
	q, err := h.repo.GetTenant(ctx, tenantID)
	if err != nil {
		if errors.Is(err, admindomain.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &q, nil
}

func (h *Handler) GetBucketQuota(ctx context.Context, backendID, bucketName string) (*admindomain.Quota, error) {
	if err := apiutil.RequireAnyRole(ctx,
		apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin, apiutil.RoleTenantAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionReadQuota,
		admindomain.Quota{BackendID: backendID, BucketName: bucketName}); err != nil {
		return nil, err
	}
	q, err := h.repo.GetBucket(ctx, backendID, bucketName)
	if err != nil {
		if errors.Is(err, admindomain.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &q, nil
}

// SetQuota upserts a quota. Tenant-scoped quota = TenantID set. Bucket-scoped
// = BackendID + BucketName set. Mask: max_total_bytes / max_object_count /
// max_bytes_per_day / max_objects_per_day.
func (h *Handler) SetQuota(ctx context.Context, q admindomain.Quota, mask []string) (*admindomain.Quota, error) {
	if err := apiutil.RequireAnyRole(ctx, apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionManageQuota, q); err != nil {
		return nil, err
	}
	// Mask is informational here — upsert writes all four caps. Future:
	// per-field UPDATE if real partial semantics needed.
	_ = mask

	tenantScope := q.TenantID != uuid.Nil
	bucketScope := q.BackendID != "" && q.BucketName != ""
	if tenantScope == bucketScope {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("exactly one of tenant_id or (backend_id, bucket_name) must be set"))
	}
	if tenantScope {
		if err := h.repo.UpsertTenant(ctx, q); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		got, err := h.repo.GetTenant(ctx, q.TenantID)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		return &got, nil
	}
	if err := h.repo.UpsertBucket(ctx, q); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	got, err := h.repo.GetBucket(ctx, q.BackendID, q.BucketName)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &got, nil
}

func (h *Handler) ResetUsage(ctx context.Context, quotaID uuid.UUID) error {
	if err := apiutil.RequireRole(ctx, apiutil.RolePlatformAdmin); err != nil {
		return err
	}
	// Cedar second guard. ResetQuotaUsage doesn't carry tenant/bucket
	// coordinates pre-load, so we only attach the principal — policies
	// that want to gate by quota_id can reference it via the action's
	// future Quota entity (slice 20+).
	if err := h.authorize(ctx, cedar.ActionResetQuotaUsage, admindomain.Quota{}); err != nil {
		return err
	}
	return h.repo.ResetDaily(ctx, quotaID, time.Now().UTC())
}
