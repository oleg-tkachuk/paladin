//go:build integration

// The postgres rate-limit backend in a real database (ADR-0046): replicas
// counting in memory share their counts through rate_limit_buckets. The unit
// suite pins the counter against an in-memory store; this pins the SQL — the
// deltas add rather than overwrite, the read-back sees the write in the same
// round trip, the runtime role can write the table with no tenant in its
// session, and the sweep keeps the live window.
//
// Requires Docker; run via `task backend:test:integration` (tags=integration).
package components

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/ratecount/pgsync"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// pgsyncInterval is never ticked here: the tests call Sync themselves.
const pgsyncInterval = time.Hour

func TestPgsyncSharesCountsAcrossReplicas(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	// The runtime role with no tenant in its session: what the sync loop of
	// an api or admin pod connects as.
	store := pgsync.NewPostgresStore(h.PoolAppNoGUC)
	a := pgsync.New(store, pgsyncInterval, zap.NewNop())
	b := pgsync.New(store, pgsyncInterval, zap.NewNop())
	const key = "tenant:shared"

	for i := 0; i < 3; i++ {
		if _, err := a.Bump(ctx, key); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Bump(ctx, key); err != nil {
		t.Fatal(err)
	}
	for _, r := range []*pgsync.Counter{a, b, a} {
		if err := r.Sync(ctx); err != nil {
			t.Fatalf("sync: %v", err)
		}
	}
	for name, r := range map[string]*pgsync.Counter{"a": a, "b": b} {
		got, _ := r.Read(ctx, key)
		if got.Current+got.Previous != 4 {
			t.Fatalf("%s sees %+v, want 4 across both buckets", name, got)
		}
	}
	var stored int64
	if err := h.PoolMigrate.QueryRow(ctx,
		`SELECT COALESCE(SUM(count), 0) FROM rate_limit_buckets WHERE key = $1`, key).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 4 {
		t.Fatalf("stored %d, want 4: a sync must add its counts, not overwrite", stored)
	}
}

func TestPgsyncSweepKeepsTheLiveWindow(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	old := now.Add(-10 * time.Minute)
	if _, err := h.PoolMigrate.Exec(ctx,
		`INSERT INTO rate_limit_buckets (key, bucket_start, count) VALUES ('token:old', $1, 1), ('token:live', $2, 1)`,
		old, now); err != nil {
		t.Fatal(err)
	}
	n, err := pgsync.NewSweeper(h.PoolAppNoGUC).SweepRateLimitBuckets(ctx, 5*time.Minute)
	if err != nil || n != 1 {
		t.Fatalf("swept %d, %v; want the one old bucket", n, err)
	}
	var left int
	if err := h.PoolMigrate.QueryRow(ctx, `SELECT count(*) FROM rate_limit_buckets WHERE key = 'token:live'`).Scan(&left); err != nil || left != 1 {
		t.Fatalf("live bucket gone: %d %v", left, err)
	}
}

// The old per-limiter tables are gone with the migration that replaced them.
func TestPgsyncReplacedTheOldBucketTables(t *testing.T) {
	h := pgharness.Setup(t)
	for _, table := range []string{"tenant_rate_buckets", "api_token_rate_buckets"} {
		var exists bool
		if err := h.PoolMigrate.QueryRow(context.Background(),
			`SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil || exists {
			t.Fatalf("%s still exists (%v)", table, err)
		}
	}
}
