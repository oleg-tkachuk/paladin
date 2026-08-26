//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// force=true skips the friendly bucket count, but buckets.backend_id is
// ON DELETE RESTRICT, so the database refuses regardless. That refusal used to
// reach the operator as a raw SQLSTATE under CodeInternal — a 500 for the
// system working as designed. It must read as a conflict, like every other
// "this thing still has children" refusal.
func TestDeleteBackendWithBucketsIsAConflictEvenWithForce(t *testing.T) {
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

	for _, force := range []bool{false, true} {
		err := repo.Delete(ctx, backendID, 0, force)
		if !errors.Is(err, admindomain.ErrConflict) {
			t.Errorf("Delete(force=%v) = %v, want ErrConflict", force, err)
		}
	}
}
