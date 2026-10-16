//go:build integration

package components

import (
	"context"
	"slices"
	"testing"

	objectkey "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/collectionh"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// onePerPage makes every page boundary also a boundary between rows.
const onePerPage = 1

// A list across tenants runs in (tenant, name) order, and a page that ends in
// one tenant resumes in the next. The token used to carry the name alone, so
// a page boundary between tenants skipped or repeated rows — on the bucket
// browser, and on the platform's collection catalogue.
func TestCollectionListPagesAcrossTenants(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewCollectionRepo(sqlc.New(pool), pool)
	readAll := auth.WithCrossTenantRead(ctx)

	seedBucketRow(t, ctx, pool)
	tenantA, _ := mkTenant(t, ctx, pool, "shared")
	tenantB, _ := mkTenant(t, ctx, pool, "shared")
	// Names that interleave across the tenants, so name order alone is wrong.
	for _, name := range []string{"m-docs", "z-logs"} {
		seedCollectionFor(t, ctx, pool, tenantA, name)
	}
	for _, name := range []string{"a-media", "n-photos"} {
		seedCollectionFor(t, ctx, pool, tenantB, name)
	}
	want := 4

	collect := func(args objectkey.ListCollectionsArgs) []string {
		t.Helper()
		var seen []string
		for range want + 1 {
			page, next, err := repo.List(readAll, args)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			for _, c := range page {
				seen = append(seen, c.TenantID.String()+"/"+c.Collection)
			}
			if next == "" {
				return seen
			}
			args.PageToken = next
		}
		t.Fatalf("list did not end after %d pages", want+1)
		return nil
	}

	for name, args := range map[string]objectkey.ListCollectionsArgs{
		"every tenant": {PageSize: onePerPage},
		"one bucket": {
			PageSize:   onePerPage,
			BackendID:  actingBackendName,
			BucketName: actingBucketName,
		},
	} {
		seen := collect(args)
		if len(seen) != want {
			t.Errorf("%s: %d rows %v, want %d", name, len(seen), seen, want)
		}
		uniq := slices.Clone(seen)
		slices.Sort(uniq)
		if len(slices.Compact(uniq)) != len(seen) {
			t.Errorf("%s: a row came twice: %v", name, seen)
		}
	}
}
