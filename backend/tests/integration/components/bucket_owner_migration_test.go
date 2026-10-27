//go:build integration

package components

import (
	"context"
	"strings"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/schema"
)

// beforeBucketOwners is the last migration under which a bucket could have no
// owner; 080 gives every bucket one and turns sharing into grants (ADR-0032).
const beforeBucketOwners = 79

// platformSlug is the bootstrap's default slug for the platform tenant, the
// one 080 gives the buckets several tenants use.
const platformSlug = "platform"

// ownerlessBucket inserts a bucket with no owner, as a database before 080
// could hold, and returns its id.
func ownerlessBucket(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	backend := "be-" + uuid.New().String()[:8]
	mustExec(t, ctx, pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, backend)
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO buckets (backend_id, name)
		SELECT id, $2 FROM storage_backends WHERE name = $1 RETURNING id`, backend, name).Scan(&id); err != nil {
		t.Fatalf("bucket %s: %v", name, err)
	}
	return id
}

func bindCollection(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant, bucket uuid.UUID) {
	t.Helper()
	mustExec(t, ctx, pool, `INSERT INTO collections (tenant_id, name, bucket_id) VALUES ($1, $2, $3)`,
		tenant, "c-"+uuid.New().String()[:8], bucket)
}

func bucketOwner(t *testing.T, ctx context.Context, pool *pgxpool.Pool, bucket uuid.UUID) uuid.UUID {
	t.Helper()
	var owner uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT owner_tenant_id FROM buckets WHERE id = $1`, bucket).Scan(&owner); err != nil {
		t.Fatalf("owner of %s: %v", bucket, err)
	}
	return owner
}

func bucketGrantees(t *testing.T, ctx context.Context, pool *pgxpool.Pool, bucket uuid.UUID) map[uuid.UUID]bool {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT tenant_id FROM bucket_grants WHERE bucket_id = $1`, bucket)
	if err != nil {
		t.Fatalf("grants of %s: %v", bucket, err)
	}
	defer rows.Close()
	out := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[id] = true
	}
	return out
}

// Migrating a database whose buckets have no owner gives each one: the one
// tenant using it, or the platform tenant with a grant for every tenant that
// uses it. Afterwards a bucket cannot be ownerless, and only its owner or a
// grantee binds a collection to it.
func TestBucketsGetOwnersAndGrantsByTheMigration(t *testing.T) {
	ctx := context.Background()
	pool := startPostgresAt(t, beforeBucketOwners)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })

	platform := uuid.New()
	mustExec(t, ctx, pool, `INSERT INTO tenants (id, slug, display_name) VALUES ($1, $2, 'Platform')`,
		platform, platformSlug)
	alone, _ := mkTenant(t, ctx, pool, "shared")
	first, _ := mkTenant(t, ctx, pool, "shared")
	second, _ := mkTenant(t, ctx, pool, "shared")
	byDefault, _ := mkTenant(t, ctx, pool, "shared")
	stranger, _ := mkTenant(t, ctx, pool, "shared")

	single := ownerlessBucket(t, ctx, pool, "single-tenant")
	bindCollection(t, ctx, pool, alone, single)
	bindCollection(t, ctx, pool, alone, single)

	several := ownerlessBucket(t, ctx, pool, "several-tenants")
	bindCollection(t, ctx, pool, first, several)
	bindCollection(t, ctx, pool, second, several)
	bindCollection(t, ctx, pool, platform, several)
	mustExec(t, ctx, pool, `INSERT INTO tenant_default_bindings (tenant_id, bucket_id, set_by) VALUES ($1, $2, 'test')`, byDefault, several)

	empty := ownerlessBucket(t, ctx, pool, "unused")

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool.Reset()

	if got := bucketOwner(t, ctx, pool, single); got != alone {
		t.Errorf("single-tenant bucket owned by %s, want its one tenant %s", got, alone)
	}
	if got := bucketGrantees(t, ctx, pool, single); len(got) != 0 {
		t.Errorf("single-tenant bucket has grants %v, want none", got)
	}
	if got := bucketOwner(t, ctx, pool, several); got != platform {
		t.Errorf("shared bucket owned by %s, want the platform tenant", got)
	}
	want := map[uuid.UUID]bool{first: true, second: true, byDefault: true}
	got := bucketGrantees(t, ctx, pool, several)
	if len(got) != len(want) || !got[first] || !got[second] || !got[byDefault] {
		t.Errorf("shared bucket grants = %v, want the tenants using it but the platform: %v", got, want)
	}
	if got := bucketOwner(t, ctx, pool, empty); got != platform {
		t.Errorf("unused bucket owned by %s, want the platform tenant", got)
	}

	// A grantee binds; a tenant with no grant is refused by name.
	bindCollection(t, ctx, pool, first, several)
	_, err := pool.Exec(ctx, `INSERT INTO collections (tenant_id, name, bucket_id) VALUES ($1, 'refused', $2)`, stranger, several)
	if !pgerr.Is(err, pgerr.CheckViolation) || !pgerr.ConstraintIs(err, schema.CollectionsBucketTenancy) {
		t.Errorf("a tenant without a grant: err = %v, want the collections_bucket_tenancy check", err)
	}
	// And a bucket with no owner cannot be written.
	if _, err := pool.Exec(ctx, `INSERT INTO buckets (backend_id, name)
		SELECT backend_id, 'no-owner' FROM buckets WHERE id = $1`, several); !pgerr.Is(err, pgerr.NotNullViolation) {
		t.Errorf("an ownerless bucket: err = %v, want a not-null violation", err)
	}
}

// With ownerless buckets used by several tenants and no tenant to own them,
// the migration stops and says what to do rather than pick an owner.
func TestBucketOwnerMigrationRefusesWithoutThePlatformTenant(t *testing.T) {
	ctx := context.Background()
	pool := startPostgresAt(t, beforeBucketOwners)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })

	ownerlessBucket(t, ctx, pool, "unused")
	err := goose.Up(db, ".")
	if err == nil {
		t.Fatal("the migration went through with no platform tenant to own an unused bucket")
	}
	if !strings.Contains(err.Error(), `no tenant has the slug "platform"`) {
		t.Errorf("err = %v, want the migration's own refusal", err)
	}
}
