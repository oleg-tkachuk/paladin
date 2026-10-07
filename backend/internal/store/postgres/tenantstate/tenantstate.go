// Package tenantstate reads whether a tenant is live, and watches for the
// statements that change it.
package tenantstate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/notify"
)

// Channel is the LISTEN/NOTIFY channel migration 047 announces every statement
// that trashes, restores or removes a tenant on.
const Channel = "tenant_state"

// Reader reads a tenant's state from the tenants table, which carries no row
// security: a tenant is a platform-level row with no tenant to scope it to.
type Reader struct{ pool *pgxpool.Pool }

// NewReader returns a Reader on pool.
func NewReader(pool *pgxpool.Pool) *Reader { return &Reader{pool: pool} }

// TenantState is the tenant's state now.
func (r *Reader) TenantState(ctx context.Context, tenantID uuid.UUID) (auth.TenantState, error) {
	var deletedAt *time.Time
	err := r.pool.QueryRow(ctx, `SELECT deleted_at FROM tenants WHERE id = $1`, tenantID).Scan(&deletedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return auth.TenantMissing, nil
	case err != nil:
		return 0, fmt.Errorf("tenantstate: read %s: %w", tenantID, err)
	case deletedAt != nil:
		return auth.TenantTrashed, nil
	}
	return auth.TenantLive, nil
}

// TenantIDBySlug finds the tenant a slug names as GetTenantBySlug does: the
// live tenant holding it, else the most recently trashed one.
func (r *Reader) TenantIDBySlug(ctx context.Context, slug string) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT id FROM tenants WHERE slug = $1
		ORDER BY deleted_at IS NOT NULL, deleted_at DESC
		LIMIT 1`, slug).Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return uuid.Nil, false, nil
	case err != nil:
		return uuid.Nil, false, fmt.Errorf("tenantstate: slug %q: %w", slug, err)
	}
	return id, true, nil
}

// NewWatcher builds a watcher that calls onChange — the cache's Clear —
// whenever a tenant is trashed, restored or removed, and after every
// reconnect.
func NewWatcher(pool *pgxpool.Pool, onChange func()) *notify.Watcher {
	return notify.New(pool, Channel, onChange)
}
