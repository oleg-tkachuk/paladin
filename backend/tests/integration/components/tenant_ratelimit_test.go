//go:build integration

package components

import (
	"context"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// The per-tenant limiter kept its buckets in each process, so a tenant's real
// ceiling was replicas × requests_per_second — 600/s at the chart's default of
// two api replicas, where the config said 300. Nothing errored; the limit was
// simply looser than the number an operator set.
//
// These exercise the SQL, because that is where the sharing lives: the bump
// and the read of the previous bucket happen in one statement, so two callers
// cannot both read a count that neither has incremented yet.
func TestTenantRateBucketsAreSharedAcrossCallers(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, pool, "shared")

	// Two stores over the same pool stand in for two pods.
	podA := adapters.NewTenantRateStore(sqlc.New(pool))
	podB := adapters.NewTenantRateStore(sqlc.New(pool))

	var last float64
	for n := range 10 {
		store := podA
		if n%2 == 1 {
			store = podB
		}
		weighted, retryAfter, err := store.BumpTenantRate(ctx, tenant)
		if err != nil {
			t.Fatalf("bump %d: %v", n, err)
		}
		if weighted <= last {
			t.Fatalf("bump %d returned %v, not greater than the previous %v — "+
				"the two callers are not sharing a window", n, weighted, last)
		}
		last = weighted
		if retryAfter <= 0 || retryAfter > 60 {
			t.Errorf("retry_after %v out of range for a one-minute bucket", retryAfter)
		}
	}
	if last < 10 {
		t.Errorf("weighted count after 10 requests = %v, want >= 10", last)
	}
}

// One tenant's traffic must not spend another's budget.
func TestTenantRateBucketsAreIsolatedPerTenant(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	noisy, _ := mkTenant(t, ctx, pool, "shared")
	quiet, _ := mkTenant(t, ctx, pool, "shared")
	store := adapters.NewTenantRateStore(sqlc.New(pool))

	for range 5 {
		if _, _, err := store.BumpTenantRate(ctx, noisy); err != nil {
			t.Fatalf("noisy bump: %v", err)
		}
	}
	weighted, _, err := store.BumpTenantRate(ctx, quiet)
	if err != nil {
		t.Fatalf("quiet bump: %v", err)
	}
	if weighted != 1 {
		t.Errorf("quiet tenant's first request saw weighted=%v, want 1 — it was "+
			"charged for the noisy tenant", weighted)
	}
}

// The sweeper has to leave the working set alone: the window reads the current
// and previous bucket, so deleting either would reset a live tenant's count.
func TestTenantRateBucketSweepKeepsTheLiveWindow(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, pool, "shared")
	store := adapters.NewTenantRateStore(sqlc.New(pool))

	for range 3 {
		if _, _, err := store.BumpTenantRate(ctx, tenant); err != nil {
			t.Fatalf("bump: %v", err)
		}
	}
	// Five minutes of grace, as the sweeper uses.
	if _, err := store.SweepTenantRateBuckets(ctx, (5 * 60 * 1_000_000)); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	weighted, _, err := store.BumpTenantRate(ctx, tenant)
	if err != nil {
		t.Fatalf("bump after sweep: %v", err)
	}
	if weighted < 4 {
		t.Errorf("weighted=%v after sweep, want >= 4 — the sweep deleted a live bucket", weighted)
	}
}

// A row must not outlive its tenant: the counters are keyed by tenant id and
// nothing else would ever clean them up after a purge.
func TestTenantRateBucketsCascadeOnTenantDelete(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, pool, "shared")
	store := adapters.NewTenantRateStore(sqlc.New(pool))

	if _, _, err := store.BumpTenantRate(ctx, tenant); err != nil {
		t.Fatalf("bump: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, tenant); err != nil {
		t.Fatalf("delete tenant: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM tenant_rate_buckets WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("%d bucket rows survived the tenant", n)
	}
}
