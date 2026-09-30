//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/platformstats"
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

	// Four caps, two thresholds each. Seeded as a loop rather than by hand:
	// the first version of this test covered the near threshold for two of
	// the four caps, and the second still missed one of the four at-limit
	// terms. The caps are written as one SQL fragment and read as a single
	// rule, so covering some of them FEELS like covering the rule — twice
	// that feeling was wrong, and a mutation run had to say so both times.
	// A loop makes a missing term impossible rather than merely unlikely.
	const cap0 = 1000
	for i := range 4 {
		var caps, atUsage, nearUsage [4]int64
		caps[i] = cap0
		atUsage[i] = cap0            // exactly at the cap — the comparison is >=
		nearUsage[i] = cap0 * 9 / 10 // exactly 90% — nearLimitRatio is 0.9
		quota(caps, atUsage)
		quota(caps, nearUsage)
	}

	// No caps at all, but real usage: tracking only, nothing enforced. This
	// is the row that fails if any `> 0` guard is relaxed to `>= 0`, because
	// usage is never negative and every uncapped row would then read as at
	// its limit.
	quota([4]int64{0, 0, 0, 0}, [4]int64{100, 5, 100, 5})

	// One below the near threshold — 899 of 1000 is 89.9%. Capped, but
	// neither near nor at. Without it, "near" could start anywhere lower and
	// nothing would object.
	quota([4]int64{cap0, 0, 0, 0}, [4]int64{899, 0, 0, 0})

	got, err := platformstats.CollectRLS(ctx, pool)
	if err != nil {
		t.Fatalf("CollectRLS: %v", err)
	}
	q, bq := got.Quotas, base.Quotas

	assertDelta(t, "quotas.total", bq.Total, q.Total, 10)
	assertDelta(t, "quotas.tenant_scoped", bq.TenantScoped, q.TenantScoped, 10)
	assertDelta(t, "quotas.bucket_scoped", bq.BucketScoped, q.BucketScoped, 0)

	// A has every cap at zero and must not count as limited.
	assertDelta(t, "quotas.with_limits", bq.WithLimits, q.WithLimits, 9)

	// B and D sit exactly on a cap; near-limit excludes at-limit, so C and E
	// are the only near rows and A and F are neither.
	assertDelta(t, "quotas.at_limit", bq.AtLimit, q.AtLimit, 4)
	assertDelta(t, "quotas.near_limit", bq.NearLimit, q.NearLimit, 4)

	// The accounting sums cover tenant-scoped rows only. Bytes: the
	// total_bytes at/near pair (1000+900), the uncapped row (100) and the
	// 89.9% row (899). Objects: the object_count pair (1000+900) and the
	// uncapped row (5). The per-day columns feed neither sum.
	assertDelta(t, "quotas.usage_total_bytes", bq.UsageTotalBytes, q.UsageTotalBytes, 2899)
	assertDelta(t, "quotas.usage_object_count", bq.UsageObjectCount, q.UsageObjectCount, 1905)
}
