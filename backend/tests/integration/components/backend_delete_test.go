//go:build integration

package components

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// A backend that still has buckets cannot be deleted: buckets.backend_id is
// ON DELETE RESTRICT. There used to be a force flag documented as overriding
// that; it could not, and all it did was skip the friendly pre-count so the
// database's refusal arrived as a raw SQLSTATE under CodeInternal — a 500 for
// the system working as designed. It must read as a conflict, like every other
// "this thing still has children" refusal.
func TestDeleteBackendWithBucketsIsAConflict(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	q := sqlc.New(pool)
	repo := adapters.NewBackendRepoV2(q, pool)

	tenant, _ := mkTenant(t, ctx, pool, "shared")
	backendID := "be-" + uuid.NewString()[:8]
	mustExec(t, ctx, pool,
		`INSERT INTO storage_backends (name, kind, provider, endpoint, region)
		 VALUES ($1, 's3-compatible', 'garage', 'http://x.invalid:3900', 'us-east-1')`,
		backendID)
	mustExec(t, ctx, pool,
		`INSERT INTO buckets (backend_id, name, owner_tenant_id)
		 SELECT id, $2, $3 FROM storage_backends WHERE name = $1`,
		backendID, "bk-"+uuid.NewString()[:8], tenant)

	if err := repo.Delete(ctx, backendID, 0); !errors.Is(err, admindomain.ErrConflict) {
		t.Errorf("Delete = %v, want ErrConflict", err)
	}
}
