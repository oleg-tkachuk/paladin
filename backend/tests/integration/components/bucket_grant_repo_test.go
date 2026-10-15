//go:build integration

package components

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// grantedBy is who the test's grants are recorded as made by.
const grantedBy = "test"

// A grant lets its tenant bind; it is idempotent, listed, refused for a
// tenant or bucket that does not exist, and not revoked while the tenant still
// has a collection in the bucket.
func TestBucketGrantRepository(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewBucketGrantRepoV2(sqlc.New(pool))
	// Revoking asks whether the tenant has collections, which RLS would hide.
	readAll := auth.WithCrossTenantRead(ctx)

	owner, _ := mkTenant(t, ctx, pool, "shared")
	other, _ := mkTenant(t, ctx, pool, "shared")
	backend := "be-" + uuid.NewString()[:8]
	const bucket = "granted-bucket"
	mustExec(t, ctx, pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, backend)
	var bucketID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO buckets (backend_id, name, owner_tenant_id)
		SELECT id, $2, $3 FROM storage_backends WHERE name = $1 RETURNING id`, backend, bucket, owner).Scan(&bucketID); err != nil {
		t.Fatalf("bucket: %v", err)
	}

	mayBind := func(tenant uuid.UUID) bool {
		t.Helper()
		ok, err := repo.MayBind(ctx, backend, bucket, tenant)
		if err != nil {
			t.Fatalf("may bind: %v", err)
		}
		return ok
	}
	if !mayBind(owner) || mayBind(other) {
		t.Fatal("before any grant only the owner may bind")
	}

	first, err := repo.Grant(ctx, backend, bucket, other, grantedBy)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	again, err := repo.Grant(ctx, backend, bucket, other, "someone else")
	if err != nil || again.GrantedBy != grantedBy || !again.GrantedAt.Equal(first.GrantedAt) {
		t.Fatalf("a second grant = %+v, %v; want the first one kept", again, err)
	}
	if !mayBind(other) {
		t.Fatal("a grantee may not bind")
	}
	grants, err := repo.ListGrants(ctx, backend, bucket)
	if err != nil || len(grants) != 1 || grants[0].TenantID != other {
		t.Fatalf("grants = %+v, %v; want the one grant", grants, err)
	}

	if _, err := repo.Grant(ctx, backend, "no-such-bucket", other, grantedBy); !errors.Is(err, admindomain.ErrNotFound) {
		t.Errorf("grant on a missing bucket: %v, want not found", err)
	}
	if _, err := repo.Grant(ctx, backend, bucket, uuid.New(), grantedBy); !errors.Is(err, admindomain.ErrNotFound) {
		t.Errorf("grant to a missing tenant: %v, want not found", err)
	}
	if _, err := repo.MayBind(ctx, backend, "no-such-bucket", other); !errors.Is(err, admindomain.ErrNotFound) {
		t.Errorf("may bind a missing bucket: %v, want not found", err)
	}

	// The grantee binds a collection; the grant now stays.
	mustExec(t, ctx, pool, `INSERT INTO collections (tenant_id, name, bucket_id) VALUES ($1, 'kept', $2)`, other, bucketID)
	if err := repo.RevokeGrant(readAll, backend, bucket, other); !errors.Is(err, admindomain.ErrBucketGrantInUse) {
		t.Fatalf("revoke while a collection remains: %v, want in use", err)
	}
	mustExec(t, ctx, pool, `DELETE FROM collections WHERE tenant_id = $1 AND name = 'kept'`, other)
	if err := repo.RevokeGrant(readAll, backend, bucket, other); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if mayBind(other) {
		t.Fatal("a revoked grant still lets the tenant bind")
	}
	if err := repo.RevokeGrant(readAll, backend, bucket, other); !errors.Is(err, admindomain.ErrNotFound) {
		t.Errorf("revoke a grant that is gone: %v, want not found", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO collections (tenant_id, name, bucket_id) VALUES ($1, 'refused', $2)`, other, bucketID)
	if !pgerr.ConstraintIs(err, "collections_bucket_tenancy") {
		t.Errorf("binding after the revoke: %v, want the tenancy check", err)
	}
}
