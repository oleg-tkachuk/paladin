//go:build integration

package components

import (
	"context"
	"sort"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// TestListBucketsUsedByTenant pins the question a tenant's own bucket list
// asks: which buckets does it store in. A shared-layout tenant owns no bucket,
// so the ownership filter listed nothing for a tenant whose collections all
// sat in shared buckets. Each relation that makes a bucket the tenant's is
// covered on its own, and so is a bucket only another tenant stores in.
func TestListBucketsUsedByTenant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewBucketRepoV2(sqlc.New(pool), pool)

	tenant, _ := mkTenant(t, ctx, pool, "shared")
	other, _ := mkTenant(t, ctx, pool, "shared")
	// Every bucket has an owner (ADR-0032). The buckets neither tenant owns
	// are a third one's, which grants the use a collection binding needs.
	platform, _ := mkTenant(t, ctx, pool, "shared")
	backendID := "be-" + uuid.New().String()[:8]
	mustExec(t, ctx, pool,
		`INSERT INTO storage_backends (name, kind, provider, endpoint, region)
		 VALUES ($1, 's3-compatible', 'garage', 'http://x.invalid:3900', 'us-east-1')`,
		backendID)

	const (
		owned        = "owned"
		bound        = "bound"
		collection   = "collection"
		othersOnly   = "others-only"
		unreferenced = "unreferenced"
	)
	insertBucket(t, ctx, pool, backendID, owned, tenant)
	for _, name := range []string{bound, collection, othersOnly, unreferenced} {
		insertBucket(t, ctx, pool, backendID, name, platform)
	}
	// A grant makes a bucket one the tenant may use, so the listing counts it
	// (ADR-0032); the collection bindings below are what the binding trigger
	// requires one for.
	grantBucket(t, ctx, pool, tenant, backendID, collection)
	grantBucket(t, ctx, pool, other, backendID, othersOnly)
	mustExec(t, ctx, pool,
		`INSERT INTO tenant_default_bindings (tenant_id, bucket_id, set_by)
		 SELECT $1, b.id, 'integration-test' FROM buckets b
		 JOIN storage_backends sb ON sb.id = b.backend_id
		 WHERE sb.name = $2 AND b.name = $3`,
		tenant, backendID, bound)
	insertCollection(t, ctx, pool, tenant, "docs", backendID, collection)
	insertCollection(t, ctx, pool, other, "docs", backendID, othersOnly)

	cases := []struct {
		name string
		args admindomain.ListBucketsArgs
		want []string
	}{
		{
			name: "used by the tenant: owned, bound by default, holding its collection",
			args: admindomain.ListBucketsArgs{BackendID: backendID, UsedByTenantID: &tenant},
			want: []string{bound, collection, owned},
		},
		{
			name: "used by another tenant: only the bucket holding its collection",
			args: admindomain.ListBucketsArgs{BackendID: backendID, UsedByTenantID: &other},
			want: []string{othersOnly},
		},
		{
			name: "owned by the tenant: ownership alone",
			args: admindomain.ListBucketsArgs{BackendID: backendID, OwnerTenantID: &tenant},
			want: []string{owned},
		},
		{
			name: "no tenant filter: every bucket",
			args: admindomain.ListBucketsArgs{BackendID: backendID},
			want: []string{bound, collection, othersOnly, owned, unreferenced},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := repo.List(ctx, tc.args)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			names := make([]string, 0, len(got))
			for _, b := range got {
				names = append(names, b.BucketName)
			}
			sort.Strings(names)
			if !equalStrings(names, tc.want) {
				t.Errorf("got %v, want %v", names, tc.want)
			}
		})
	}
}

// TestListBucketsUsedByTenantUnderRLS reads through a pool like the runtime's:
// RLS on, no BYPASSRLS. Default bindings and collections are visible only to
// their own tenant, so the listing must run as the tenant it asks about — as
// the caller (a platform admin of another tenant) the same query finds only
// what is platform configuration: the buckets the tenant owns or holds a grant
// on (ADR-0032), and not the one it is bound to by default.
func TestListBucketsUsedByTenantUnderRLS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)

	platform, _ := mkTenant(t, ctx, admin, "shared")
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	backendID := "be-" + uuid.New().String()[:8]
	mustExec(t, ctx, admin,
		`INSERT INTO storage_backends (name, kind, provider, endpoint, region)
		 VALUES ($1, 's3-compatible', 'garage', 'http://x.invalid:3900', 'us-east-1')`,
		backendID)
	const (
		bound      = "bound"
		collection = "collection"
	)
	// The caller's tenant owns both buckets and grants the tenant the use its
	// collection binding needs (ADR-0032).
	insertBucket(t, ctx, admin, backendID, bound, platform)
	insertBucket(t, ctx, admin, backendID, collection, platform)
	grantBucket(t, ctx, admin, tenant, backendID, collection)
	mustExec(t, ctx, admin,
		`INSERT INTO tenant_default_bindings (tenant_id, bucket_id, set_by)
		 SELECT $1, b.id, 'integration-test' FROM buckets b
		 JOIN storage_backends sb ON sb.id = b.backend_id
		 WHERE sb.name = $2 AND b.name = $3`,
		tenant, backendID, bound)
	insertCollection(t, ctx, admin, tenant, "docs", backendID, collection)

	pool := rlsPool(t, ctx, admin)
	repo := adapters.NewBucketRepoV2(sqlc.New(pool), pool)
	asCaller := auth.WithPrincipal(ctx, &auth.Principal{TenantID: platform})
	args := admindomain.ListBucketsArgs{BackendID: backendID, UsedByTenantID: &tenant}

	list := func(c context.Context) []string {
		t.Helper()
		got, _, err := repo.List(c, args)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		names := make([]string, 0, len(got))
		for _, b := range got {
			names = append(names, b.BucketName)
		}
		sort.Strings(names)
		return names
	}

	if got := list(asCaller); !equalStrings(got, []string{collection}) {
		t.Errorf("as the caller: got %v, want only the granted bucket — RLS no longer "+
			"hides another tenant's default binding, so this test no longer pins why "+
			"the handler acts", got)
	}
	want := []string{bound, collection}
	if got := list(auth.WithActingTenant(asCaller, tenant)); !equalStrings(got, want) {
		t.Errorf("acting as the tenant: got %v, want %v", got, want)
	}
}

func insertBucket(t *testing.T, ctx context.Context, pool *pgxpool.Pool, backendID, name string, owner uuid.UUID) {
	t.Helper()
	mustExec(t, ctx, pool,
		`INSERT INTO buckets (backend_id, name, owner_tenant_id)
		 SELECT id, $2, $3 FROM storage_backends WHERE name = $1`,
		backendID, name, owner)
}

func insertCollection(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant uuid.UUID, name, backendID, bucket string) {
	t.Helper()
	mustExec(t, ctx, pool,
		`INSERT INTO collections (tenant_id, name, bucket_id)
		 SELECT $1, $2, b.id FROM buckets b
		 JOIN storage_backends sb ON sb.id = b.backend_id
		 WHERE sb.name = $3 AND b.name = $4`,
		tenant, name, backendID, bucket)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
