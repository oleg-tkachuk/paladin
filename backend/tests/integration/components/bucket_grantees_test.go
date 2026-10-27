//go:build integration

package components

import (
	"context"
	"slices"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// listPageSize is large enough for every bucket this test creates.
const listPageSize = 100

// Every read of a bucket carries its grantees, oldest grant first, so a list
// can say who shares a bucket without a grant lookup per row.
func TestBucketReadsCarryTheirGrantees(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	q := sqlc.New(pool)
	buckets := adapters.NewBucketRepoV2(q, pool)
	grants := adapters.NewBucketGrantRepoV2(q)

	owner, _ := mkTenant(t, ctx, pool, "shared")
	first, _ := mkTenant(t, ctx, pool, "shared")
	second, _ := mkTenant(t, ctx, pool, "shared")
	backend := "be-" + uuid.New().String()[:8]
	const shared, private = "shared-bucket", "private-bucket"
	mustExec(t, ctx, pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, backend)
	for _, name := range []string{shared, private} {
		mustExec(t, ctx, pool, `INSERT INTO buckets (backend_id, name, owner_tenant_id)
			SELECT id, $2, $3 FROM storage_backends WHERE name = $1`, backend, name, owner)
	}
	for _, tenant := range []uuid.UUID{first, second} {
		if _, err := grants.Grant(ctx, backend, shared, tenant, grantedBy); err != nil {
			t.Fatalf("grant: %v", err)
		}
	}
	want := []uuid.UUID{first, second}

	got, err := buckets.Get(ctx, backend, shared)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !slices.Equal(got.GrantedTenantIDs, want) {
		t.Errorf("get: grantees %v, want %v", got.GrantedTenantIDs, want)
	}
	if got, err := buckets.Get(ctx, backend, private); err != nil || len(got.GrantedTenantIDs) != 0 {
		t.Errorf("get of an ungranted bucket: grantees %v, %v; want none", got.GrantedTenantIDs, err)
	}

	listed, _, err := buckets.List(ctx, admindomain.ListBucketsArgs{BackendID: backend, PageSize: listPageSize})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byName := map[string][]uuid.UUID{}
	for _, b := range listed {
		byName[b.BucketName] = b.GrantedTenantIDs
	}
	if !slices.Equal(byName[shared], want) || len(byName[private]) != 0 {
		t.Errorf("list: grantees %v, want %v on %s and none on %s", byName, want, shared, private)
	}

	accessible, _, err := buckets.ListAccessible(ctx, first, listPageSize, "", "")
	if err != nil {
		t.Fatalf("list accessible: %v", err)
	}
	if len(accessible) != 1 || !slices.Equal(accessible[0].GrantedTenantIDs, want) {
		t.Errorf("list accessible: %+v, want %s with grantees %v", accessible, shared, want)
	}
}
