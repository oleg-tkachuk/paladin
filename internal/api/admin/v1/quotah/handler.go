// Package quotah implements the admin QuotaService — set/get usage caps.
package quotah

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
)

type Handler struct {
	repo admindomain.QuotaRepository
}

func NewHandler(r admindomain.QuotaRepository) *Handler { return &Handler{repo: r} }

func (h *Handler) GetTenantQuota(ctx context.Context, tenantID uuid.UUID) (*admindomain.Quota, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) && tenantID != caller {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("cross-tenant denied"))
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
	return h.repo.ResetDaily(ctx, quotaID, time.Now().UTC())
}
