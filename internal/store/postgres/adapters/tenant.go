package adapters

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// TenantRepo satisfies tenant.Repository. The raw pool is needed because the
// sqlc DeleteTenant query bakes in an OCC guard (`AND resource_version = $2`)
// and the current DeleteTenantRequest proto has no resource_version field —
// see Delete below for the unconditional path.
type TenantRepo struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool
}

func NewTenantRepo(q *sqlc.Queries, pool *pgxpool.Pool) *TenantRepo {
	return &TenantRepo{q: q, pool: pool}
}

var _ tenant.Repository = (*TenantRepo)(nil)

func (r *TenantRepo) Create(ctx context.Context, args tenant.CreateTenantArgs) (tenant.Tenant, error) {
	if err := r.q.CreateTenant(ctx,
		pgUUID(args.TenantID),
		strPtr(args.DisplayName),
		args.Labels,
		args.InheritedCedarPolicy,
	); err != nil {
		return tenant.Tenant{}, fmt.Errorf("create tenant: %w", err)
	}
	return r.Get(ctx, args.TenantID)
}

func (r *TenantRepo) Get(ctx context.Context, tenantID uuid.UUID) (tenant.Tenant, error) {
	row, err := r.q.GetTenant(ctx, pgUUID(tenantID))
	if err != nil {
		return tenant.Tenant{}, err
	}
	return tenantFromSQLC(row.Tenant), nil
}

func (r *TenantRepo) Update(ctx context.Context, args tenant.UpdateTenantArgs) (tenant.Tenant, error) {
	var policyHash []byte
	if args.InheritedCedarPolicy != nil {
		sum := sha256.Sum256([]byte(*args.InheritedCedarPolicy))
		policyHash = sum[:]
	}
	rows, err := r.q.UpdateTenant(ctx,
		pgUUID(args.TenantID),
		args.DisplayName,
		args.Labels,
		args.InheritedCedarPolicy,
		policyHash,
		args.ExpectedVersion,
	)
	if err != nil {
		return tenant.Tenant{}, fmt.Errorf("update tenant: %w", err)
	}
	if rows == 0 {
		return tenant.Tenant{}, tenant.ErrVersionMismatch
	}
	return r.Get(ctx, args.TenantID)
}

func (r *TenantRepo) Delete(ctx context.Context, tenantID uuid.UUID, expectedVersion int64) error {
	// expectedVersion == 0 means "no OCC guard" — DeleteTenantRequest doesn't
	// carry a resource_version, so the connect shim always passes 0. Drop the
	// version predicate in that case; sqlc's DeleteTenant hardcodes it.
	if expectedVersion == 0 {
		tag, err := r.pool.Exec(ctx, `DELETE FROM tenants WHERE tenant_id = $1`, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("delete tenant: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return tenant.ErrNotFound
		}
		return nil
	}
	rows, err := r.q.DeleteTenant(ctx, pgUUID(tenantID), expectedVersion)
	if err != nil {
		return fmt.Errorf("delete tenant: %w", err)
	}
	if rows == 0 {
		return tenant.ErrVersionMismatch
	}
	return nil
}

func (r *TenantRepo) List(ctx context.Context, pageSize int32, afterID uuid.UUID) ([]tenant.Tenant, string, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	rows, err := r.q.ListTenants(ctx, pgUUID(afterID), pageSize)
	if err != nil {
		return nil, "", fmt.Errorf("list tenants: %w", err)
	}
	out := make([]tenant.Tenant, 0, len(rows))
	for _, row := range rows {
		out = append(out, tenantFromSQLC(row.Tenant))
	}
	var next string
	if int32(len(out)) == pageSize && len(out) > 0 {
		next = out[len(out)-1].TenantID.String()
	}
	return out, next, nil
}

func tenantFromSQLC(t sqlc.Tenant) tenant.Tenant {
	return tenant.Tenant{
		TenantID:             uuidFrom(t.TenantID),
		DisplayName:          derefStr(t.DisplayName),
		Labels:               t.Labels,
		InheritedCedarPolicy: t.InheritedCedarPolicy,
		InheritedPolicyHash:  t.InheritedPolicyHash,
		ResourceVersion:      t.ResourceVersion,
		CreatedAt:            timeFrom(t.CreatedAt),
		UpdatedAt:            timeFrom(t.UpdatedAt),
	}
}
