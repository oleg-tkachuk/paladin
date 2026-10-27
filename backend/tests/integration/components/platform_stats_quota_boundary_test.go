//go:build integration

package components

import (
	"context"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/platformstats"
)

// The quota census decides what the console's /stats page reports as a fleet
// in breach, and every one of its decisions is a comparison inside a SQL
// string. A mutation run over internal/platformstats caught 52% and left
// nine survivors in this one function alone — every boundary operator in the
// at-limit, near-limit and has-limits filters could be relaxed or tightened
// without a single test noticing.
//
// The contract (migrations/070): a NULL cap is no cap, and a cap of 0 allows
// nothing. platformstats.go guards every term with IS NOT NULL.
//
// What that buys, concretely: drop one guard and the NULL comparison already
// reads false, but read NULL as 0 (a COALESCE, say) and every uncapped tenant
// is counted at its limit — usage is never negative — and the page reports a
// fleet in breach that is not. A cap of 0, on the other hand, is a limit
// reached from the start, and must count as one.
//
// Each row below sits exactly ON a boundary, because a row comfortably
// inside one proves nothing about which way the comparison points.
func TestCollectQuotas_CountsExactlyOnEachBoundary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)

	base, err := platformstats.CollectRLS(ctx, pool, platformstats.TenantPage{})
	if err != nil {
		t.Fatalf("baseline census: %v", err)
	}

	// quotas carries a UNIQUE on tenant_id, so each row needs its own tenant.
	// nearLimitRatio is 0.9, so "near" starts at exactly 90% of a cap.
	quota := func(caps [4]*int64, usage [4]int64) {
		t.Helper()
		hex := uuid.New().String()[:8]
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
		var caps [4]*int64
		var atUsage, nearUsage [4]int64
		caps[i] = new(int64(cap0))
		atUsage[i] = cap0            // exactly at the cap — the comparison is >=
		nearUsage[i] = cap0 * 9 / 10 // exactly 90% — nearLimitRatio is 0.9
		quota(caps, atUsage)
		quota(caps, nearUsage)
	}

	// No caps at all, but real usage: tracking only, nothing enforced. This
	// is the row that fails if a NULL cap is ever read as 0, because usage is
	// never negative and every uncapped row would then read as at its limit.
	quota([4]*int64{}, [4]int64{100, 5, 100, 5})

	// A cap of 0 with no usage: nothing is allowed, so the row is at its
	// limit before anything is stored, and it has a limit.
	quota([4]*int64{nil, new(int64(0)), nil, nil}, [4]int64{})

	// One below the near threshold — 899 of 1000 is 89.9%. Capped, but
	// neither near nor at. Without it, "near" could start anywhere lower and
	// nothing would object.
	quota([4]*int64{new(int64(cap0))}, [4]int64{899, 0, 0, 0})

	got, err := platformstats.CollectRLS(ctx, pool, platformstats.TenantPage{})
	if err != nil {
		t.Fatalf("CollectRLS: %v", err)
	}
	q, bq := got.Quotas, base.Quotas

	assertDelta(t, "quotas.total", bq.Total, q.Total, 11)
	assertDelta(t, "quotas.tenant_scoped", bq.TenantScoped, q.TenantScoped, 11)
	assertDelta(t, "quotas.bucket_scoped", bq.BucketScoped, q.BucketScoped, 0)

	// The uncapped row must not count as limited; the zero-cap row must.
	assertDelta(t, "quotas.with_limits", bq.WithLimits, q.WithLimits, 10)

	// The four rows exactly on a cap, and the zero-cap row; near-limit
	// excludes at-limit, so the four 90% rows are the only near ones.
	assertDelta(t, "quotas.at_limit", bq.AtLimit, q.AtLimit, 5)
	assertDelta(t, "quotas.near_limit", bq.NearLimit, q.NearLimit, 4)

	// The accounting sums cover tenant-scoped rows only. Bytes: the
	// total_bytes at/near pair (1000+900), the uncapped row (100) and the
	// 89.9% row (899). Objects: the object_count pair (1000+900) and the
	// uncapped row (5). The per-day columns feed neither sum.
	assertDelta(t, "quotas.usage_total_bytes", bq.UsageTotalBytes, q.UsageTotalBytes, 2899)
	assertDelta(t, "quotas.usage_object_count", bq.UsageObjectCount, q.UsageObjectCount, 1905)
}
