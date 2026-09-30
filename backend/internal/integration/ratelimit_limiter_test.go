//go:build integration

// The Postgres rate limiter is the only thing standing between a leaked API
// token and unbounded use of the API it opens. It had no test.
//
// Its arithmetic is a sliding window approximated from two fixed buckets, so
// the assertions here avoid pinning exact weighted values — those depend on
// where in the wall-clock minute the test runs. What is pinned is the
// behaviour that has to hold at any instant: the unlimited fast path writes
// nothing, the counter refuses past capacity, a denial always carries a usable
// RetryAfter, reads do not mutate, and the sweep respects its cutoff.
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	ratelimitstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token/ratelimit/postgres"
)

// newLimiterFixture seeds a tenant and a token, because api_token_rate_buckets
// carries a foreign key to api_tokens — a limiter test on an invented uuid
// would exercise a path production never takes.
func newLimiterFixture(t *testing.T) (context.Context, *pgxpool.Pool, *ratelimitstore.Limiter, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	pool := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, pool, "shared")

	tok := mkToken(tenant, "limited", time.Now().Add(time.Hour))
	if err := newTokenStore(t, pool).Insert(ctx, tok, []byte("digest-limiter")); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	lim, err := ratelimitstore.New(pool)
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	return ctx, pool, lim, tok.ID
}

func bucketRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tokenID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM api_token_rate_buckets WHERE token_id = $1`, tokenID).Scan(&n); err != nil {
		t.Fatalf("count buckets: %v", err)
	}
	return n
}

// TestLimiterUnlimitedFastPath pins that capacity ≤ 0 means unlimited and
// touches no storage. Inverting this sense would either rate-limit every
// token without an explicit RPM or write a row per request for tokens that
// were meant to be free.
func TestLimiterUnlimitedFastPath(t *testing.T) {
	ctx, pool, lim, tokenID := newLimiterFixture(t)

	for _, capacity := range []int{0, -1} {
		d, err := lim.Allow(ctx, tokenID, capacity)
		if err != nil {
			t.Fatalf("allow(capacity=%d): %v", capacity, err)
		}
		if !d.Allowed {
			t.Errorf("capacity=%d denied a request", capacity)
		}
	}
	if n := bucketRows(t, ctx, pool, tokenID); n != 0 {
		t.Errorf("unlimited path wrote %d bucket rows, want 0", n)
	}
}

// TestLimiterDeniesPastCapacity pins the core decision. The bump is
// unconditional and happens before the check, so the Nth call at capacity N is
// the last allowed one.
func TestLimiterDeniesPastCapacity(t *testing.T) {
	ctx, _, lim, tokenID := newLimiterFixture(t)
	const capacity = 3

	for i := 1; i <= capacity; i++ {
		d, err := lim.Allow(ctx, tokenID, capacity)
		if err != nil {
			t.Fatalf("allow %d: %v", i, err)
		}
		if !d.Allowed {
			t.Fatalf("request %d of %d denied (weighted %v)", i, capacity, d.WeightedCount)
		}
	}

	d, err := lim.Allow(ctx, tokenID, capacity)
	if err != nil {
		t.Fatalf("over-capacity allow: %v", err)
	}
	if d.Allowed {
		t.Fatalf("request %d allowed at capacity %d (weighted %v)", capacity+1, capacity, d.WeightedCount)
	}
	// A denial without a usable RetryAfter makes clients busy-loop.
	if d.RetryAfter < time.Second || d.RetryAfter > time.Minute {
		t.Errorf("retry_after %v is outside the one-minute window", d.RetryAfter)
	}
	if d.WeightedCount <= float64(capacity) {
		t.Errorf("denied with weighted count %v, which is not over capacity %d", d.WeightedCount, capacity)
	}
}

// TestLimiterCountsPerToken pins that buckets are keyed by token: one token
// exhausting its quota must not deny another.
func TestLimiterCountsPerToken(t *testing.T) {
	ctx, pool, lim, tokenA := newLimiterFixture(t)

	var tenant uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT tenant_id FROM api_tokens WHERE id = $1`, tokenA).Scan(&tenant); err != nil {
		t.Fatalf("read tenant: %v", err)
	}
	tokB := mkToken(tenant, "other", time.Now().Add(time.Hour))
	if err := newTokenStore(t, pool).Insert(ctx, tokB, []byte("digest-other")); err != nil {
		t.Fatalf("seed second token: %v", err)
	}

	for i := 0; i < 4; i++ {
		if _, err := lim.Allow(ctx, tokenA, 1); err != nil {
			t.Fatalf("exhaust A: %v", err)
		}
	}
	d, err := lim.Allow(ctx, tokB.ID, 1)
	if err != nil {
		t.Fatalf("allow B: %v", err)
	}
	if !d.Allowed {
		t.Error("token B denied because token A exhausted its own bucket")
	}
}

// TestLimiterUsageDoesNotMutate pins the property the package comment claims
// and the console depends on: a dashboard polling Usage every few seconds must
// not spend the quota it is displaying.
func TestLimiterUsageDoesNotMutate(t *testing.T) {
	ctx, _, lim, tokenID := newLimiterFixture(t)

	for i := 0; i < 3; i++ {
		if _, err := lim.Allow(ctx, tokenID, 100); err != nil {
			t.Fatalf("allow %d: %v", i, err)
		}
	}

	first, err := lim.Usage(ctx, tokenID)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if first.CurrentBucketCount != 3 {
		t.Fatalf("current bucket %d, want 3", first.CurrentBucketCount)
	}
	if first.WindowResetsAt.Before(time.Now().UTC()) {
		t.Errorf("window resets in the past: %v", first.WindowResetsAt)
	}

	for i := 0; i < 5; i++ {
		if _, err := lim.Usage(ctx, tokenID); err != nil {
			t.Fatalf("repeat usage: %v", err)
		}
	}
	after, err := lim.Usage(ctx, tokenID)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if after.CurrentBucketCount != first.CurrentBucketCount {
		t.Errorf("Usage bumped the counter: %d → %d", first.CurrentBucketCount, after.CurrentBucketCount)
	}

	// An untouched token reads as empty rather than erroring.
	empty, err := lim.Usage(ctx, uuid.New())
	if err != nil {
		t.Fatalf("usage of unknown token: %v", err)
	}
	if empty.CurrentBucketCount != 0 || empty.WeightedCount != 0 {
		t.Errorf("unknown token reported usage: %+v", empty)
	}
}

// TestLimiterSweepRespectsCutoff pins that the sweeper drops only buckets
// outside the window it is told to keep. Sweeping the current bucket would
// hand a rate-limited caller a fresh quota on demand.
func TestLimiterSweepRespectsCutoff(t *testing.T) {
	ctx, pool, lim, tokenID := newLimiterFixture(t)

	if _, err := lim.Allow(ctx, tokenID, 100); err != nil {
		t.Fatalf("allow: %v", err)
	}
	// A bucket from an hour ago, which no window can still weight.
	mustExec(t, ctx, pool,
		`INSERT INTO api_token_rate_buckets (token_id, bucket_start, count)
		 VALUES ($1, date_trunc('minute', now()) - interval '1 hour', 7)`, tokenID)

	if n := bucketRows(t, ctx, pool, tokenID); n != 2 {
		t.Fatalf("setup left %d buckets, want 2", n)
	}

	swept, err := lim.Sweep(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if swept != 1 {
		t.Fatalf("swept %d rows, want 1", swept)
	}
	if n := bucketRows(t, ctx, pool, tokenID); n != 1 {
		t.Errorf("%d buckets remain, want 1 (the current one)", n)
	}

	got, err := lim.Usage(ctx, tokenID)
	if err != nil {
		t.Fatalf("usage after sweep: %v", err)
	}
	if got.CurrentBucketCount != 1 {
		t.Errorf("sweep disturbed the current bucket: count %d, want 1", got.CurrentBucketCount)
	}
}

// TestLimiterRejectsNilPool pins the constructor guard — a nil pool would
// otherwise surface as a panic on the first rate-limited request rather than
// at wiring time.
func TestLimiterRejectsNilPool(t *testing.T) {
	if _, err := ratelimitstore.New(nil); err == nil {
		t.Fatal("nil pool accepted")
	}
}
