//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/tests/integration/pgharness"
)

// Every List in the adapters package normalises its page size with
// `if pageSize <= 0 { pageSize = 50 }`, eleven times over. The clamp is
// load-bearing, not defensive: the admin shims forward
// `req.Msg.GetPage().GetPageSize()` straight through, and an unset `page`
// message is a zero — so the clamp is the only thing standing between "the
// client omitted page_size" and `LIMIT 0`.
//
// Relaxed to `< 0` the guard still compiles, still looks like a bounds check,
// and returns an empty page for a table with rows in it. Nothing fails: the
// caller is told there is nothing there, which is the same shape as the
// pushdown bug this package already carries a comment about.
//
// This holds the shared shape at two of the eleven sites. The rest need their
// own fixtures; BACKLOG carries the list.
func TestListPageSizeZeroMeansDefault(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	q := sqlc.New(h.PoolMigrate)

	tenantID := mustCreateTenant(t, h.PoolMigrate, "pagesize-tenant")
	seedBackend(t, h.PoolMigrate, "pagesize-be")
	mustSeedBucketAndKey(t, h.PoolMigrate, tenantID, "pagesize-be", "pagesize-bucket", "docs")

	t.Run("buckets", func(t *testing.T) {
		repo := adapters.NewBucketRepoV2(q, h.PoolMigrate)

		// An explicit page size returns the row, so the zero case below is
		// about the clamp and not about an empty table.
		explicit, _, err := repo.List(ctx, admindomain.ListBucketsArgs{PageSize: 10})
		if err != nil {
			t.Fatalf("list with explicit page size: %v", err)
		}
		if len(explicit) == 0 {
			t.Fatal("fixture returned no buckets with PageSize=10 — the test below could not fail")
		}

		got, _, err := repo.List(ctx, admindomain.ListBucketsArgs{})
		if err != nil {
			t.Fatalf("list with unset page size: %v", err)
		}
		if len(got) != len(explicit) {
			t.Errorf("unset page size returned %d buckets, want %d — zero must mean the default, not LIMIT 0",
				len(got), len(explicit))
		}
	})

	t.Run("backends", func(t *testing.T) {
		repo := adapters.NewBackendRepoV2(q, h.PoolMigrate)

		explicit, _, err := repo.List(ctx, 10, "", "")
		if err != nil {
			t.Fatalf("list with explicit page size: %v", err)
		}
		if len(explicit) == 0 {
			t.Fatal("fixture returned no backends with pageSize=10 — the test below could not fail")
		}

		got, _, err := repo.List(ctx, 0, "", "")
		if err != nil {
			t.Fatalf("list with zero page size: %v", err)
		}
		if len(got) != len(explicit) {
			t.Errorf("zero page size returned %d backends, want %d", len(got), len(explicit))
		}
	})
}
