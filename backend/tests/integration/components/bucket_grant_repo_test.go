//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	objectkey "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/collectionh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/schema"
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
	backend := "be-" + uuid.New().String()[:8]
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
	if !pgerr.ConstraintIs(err, schema.CollectionsBucketTenancy) {
		t.Errorf("binding after the revoke: %v, want the tenancy check", err)
	}
}

// A collection create the binding trigger refuses comes back as
// ErrBucketNotGranted, which the API answers PERMISSION_DENIED — not as an
// unclassified database error, which it answered Internal.
func TestCollectionCreateRefusedWithoutAGrant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	owner, _ := mkTenant(t, ctx, pool, "shared")
	other, _ := mkTenant(t, ctx, pool, "shared")
	backend := "be-" + uuid.New().String()[:8]
	const bucket = "owned-bucket"
	mustExec(t, ctx, pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, backend)
	mustExec(t, ctx, pool, `INSERT INTO buckets (backend_id, name, owner_tenant_id)
		SELECT id, $2, $3 FROM storage_backends WHERE name = $1`, backend, bucket, owner)

	_, err := adapters.NewCollectionRepo(sqlc.New(pool), pool).Create(ctx, objectkey.CreateCollectionArgs{
		TenantID: other, Collection: "refused", BackendID: backend, BucketName: bucket,
	})
	if !errors.Is(err, admindomain.ErrBucketNotGranted) {
		t.Fatalf("create in a bucket the tenant may not bind: %v, want ErrBucketNotGranted", err)
	}
}

// A default binding names a bucket the tenant may bind to: one it owns or
// holds a grant on. A tenant created on the operator's pool is granted it in
// the same transaction, so its first collection binds.
func TestDefaultBindingNeedsAGrant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	q := sqlc.New(pool)
	tenants := adapters.NewTenantRepo(q, pool)
	grants := adapters.NewBucketGrantRepoV2(q)

	operator, _ := mkTenant(t, ctx, pool, "shared")
	stranger, _ := mkTenant(t, ctx, pool, "shared")
	backend := "be-" + uuid.New().String()[:8]
	const pooled = "operator-pool"
	mustExec(t, ctx, pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, backend)
	mustExec(t, ctx, pool, `INSERT INTO buckets (backend_id, name, owner_tenant_id)
		SELECT id, $2, $3 FROM storage_backends WHERE name = $1`, backend, pooled, operator)
	resource := "storageBackends/" + backend + "/buckets/" + pooled

	if _, err := tenants.SetDefaultBinding(ctx, stranger, resource, grantedBy); !errors.Is(err, admindomain.ErrBucketNotGranted) {
		t.Fatalf("binding a tenant by default to a bucket it may not bind: %v, want ErrBucketNotGranted", err)
	}
	if _, err := grants.Grant(ctx, backend, pooled, stranger, grantedBy); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if _, err := tenants.SetDefaultBinding(ctx, stranger, resource, grantedBy); err != nil {
		t.Fatalf("binding by default once granted: %v", err)
	}

	hex := uuid.New().String()[:8]
	created, err := tenants.Create(ctx, tenanth.CreateTenantArgs{
		TenantID: uuid.New(), Slug: "t-" + hex, DisplayName: "tn-" + hex, StorageLayout: "shared",
		DefaultBackendID: backend, DefaultBucketName: pooled,
	})
	if err != nil {
		t.Fatalf("create a tenant on the pool: %v", err)
	}
	if ok, err := grants.MayBind(ctx, backend, pooled, created.TenantID); err != nil || !ok {
		t.Fatalf("the new tenant may bind = %v, %v; want the pool granted to it", ok, err)
	}
}
