package adapters

import (
	"context"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/systemh"
	"github.com/oleg-tkachuk/paladin/backend/internal/platformstats"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// SystemInventory is systemh.Inventory over the admin pod's pool.
type SystemInventory struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
}

// NewSystemInventory reads through pool; tenants carries no RLS policy.
func NewSystemInventory(pool *pgxpool.Pool) *SystemInventory {
	return &SystemInventory{pool: pool, q: sqlc.New(pool)}
}

// ControlPlane is the control-plane census. ctx carries the cross-tenant
// read flag the caller set.
func (s *SystemInventory) ControlPlane(ctx context.Context) (*platformstats.ControlPlane, error) {
	return platformstats.CollectControlPlane(ctx, s.pool)
}

// TenantNames labels the tenants in ids by their primary key. The ids come
// from the worker's census; one that is not a UUID names no tenant and is
// left out, as one whose tenant row is gone.
func (s *SystemInventory) TenantNames(ctx context.Context, ids []string) (map[string]systemh.TenantName, error) {
	keys := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		u, err := uuid.Parse(id)
		if err != nil {
			continue
		}
		keys = append(keys, pgUUID(u))
	}
	rows, err := s.q.TenantNamesByID(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("tenant names: %w", err)
	}
	out := make(map[string]systemh.TenantName, len(rows))
	for _, r := range rows {
		out[uuidFrom(r.ID).String()] = systemh.TenantName{Slug: r.Slug, DisplayName: r.DisplayName}
	}
	return out, nil
}
