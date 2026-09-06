//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/platformstats"
)

// The quota census decides what the console's /stats page reports as a fleet
// in breach, and every one of its decisions is a comparison inside a SQL
// string. A mutation run over internal/platformstats caught 52% and left
// nine survivors in this one function alone — every boundary operator in the
// at-limit, near-limit and has-limits filters could be relaxed or tightened
// without a single test noticing.
//
// The contract is stated twice in the source and enforced nowhere:
//
//	migrations/001: "0 means 'no cap' — the convention the whole codebase
//	reads these with (max_x > 0 AND usage_x >= max_x)"
//
//	platformstats.go: "A cap of 0 means 'no cap' throughout the schema,
//	hence the `> 0` guard on every term"
//
// What that buys, concretely: relax one `> 0` to `>= 0` and an absent cap
// becomes a cap of zero. Usage is never negative, so usage >= 0 always holds
// and EVERY uncapped tenant is counted at its limit — the page reports a
// fleet in breach that is not.
//
// Each row below sits exactly ON a boundary, because a row comfortably
// inside one proves nothing about which way the comparison points.
func TestCollectQuotas_CountsExactlyOnEachBoundary(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)

	base, err := platformstats.CollectRLS(ctx, pool)
	if err != nil {
		t.Fatalf("baseline census: %v", err)
	}

	// quotas carries a UNIQUE on tenant_id, so each row needs its own tenant.
	// nearLimitRatio is 0.9, so "near" starts at exactly 90% of a cap.
	quota := func(caps, usage [4]int64) {
		t.Helper()
		hex := uuid.NewString()[:8]
		tenantID := uuid.New()
		mustExec(t, ctx, pool, `INSERT INTO tenants (id, slug, display_name) VALUES ($1, $2, $3)`,
			tenantID, "t-"+hex, "tn-"+hex)
		mustExec(t, ctx, pool, `
			INSERT INTO quotas (tenant_id,
				max_total_bytes, max_object_count, max_bytes_per_day, max_objects_per_day,
				usage_total_bytes, usage_object_count, usage_bytes_today, usage_objects_today)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			tenantID, caps[0], caps[1], caps[2], caps[3],
			usage[0], usage[1], usage[2], usage[3])
	}

	// A: no caps at all, but real usage. Usage tracking only — nothing is
	//    enforced, so it is neither at nor near a limit, and it does not
	//    count as having limits. This is the row that fails if any `> 0`
	//    guard is relaxed to `>= 0`.
	quota([4]int64{0, 0, 0, 0}, [4]int64{100, 5, 100, 5})

	// B: usage EQUALS the byte cap. At limit — the comparison is `>=`.
	quota([4]int64{1000, 0, 0, 0}, [4]int64{1000, 0, 0, 0})

	// C: usage is exactly 90% of the object-count cap. Near limit, not at it.
	quota([4]int64{0, 1000, 0, 0}, [4]int64{0, 900, 0, 0})

	// D: the per-day byte cap, reached exactly. At limit.
	quota([4]int64{0, 0, 1000, 0}, [4]int64{0, 0, 1000, 0})

	// E: the per-day object cap, at exactly 90%. Near limit.
	quota([4]int64{0, 0, 0, 1000}, [4]int64{0, 0, 0, 900})

	// F: one below the near threshold — 899 of 1000 is 89.9%. Capped, but
	//    neither near nor at. Without this row, "near" could start anywhere
	//    below 90% and nothing would object.
	quota([4]int64{1000, 0, 0, 0}, [4]int64{899, 0, 0, 0})

	got, err := platformstats.CollectRLS(ctx, pool)
	if err != nil {
		t.Fatalf("CollectRLS: %v", err)
	}
	q, bq := got.Quotas, base.Quotas

	assertDelta(t, "quotas.total", bq.Total, q.Total, 6)
	assertDelta(t, "quotas.tenant_scoped", bq.TenantScoped, q.TenantScoped, 6)
	assertDelta(t, "quotas.bucket_scoped", bq.BucketScoped, q.BucketScoped, 0)

	// A has every cap at zero and must not count as limited.
	assertDelta(t, "quotas.with_limits", bq.WithLimits, q.WithLimits, 5)

	// B and D sit exactly on a cap; near-limit excludes at-limit, so C and E
	// are the only near rows and A and F are neither.
	assertDelta(t, "quotas.at_limit", bq.AtLimit, q.AtLimit, 2)
	assertDelta(t, "quotas.near_limit", bq.NearLimit, q.NearLimit, 2)

	// The accounting sums cover tenant-scoped rows only: 100+1000+899 bytes
	// and 5+900 objects across the six rows above.
	assertDelta(t, "quotas.usage_total_bytes", bq.UsageTotalBytes, q.UsageTotalBytes, 1999)
	assertDelta(t, "quotas.usage_object_count", bq.UsageObjectCount, q.UsageObjectCount, 905)
}
