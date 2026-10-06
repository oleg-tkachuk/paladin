//go:build integration

// Verifies the two partitioned tables — audit_log (monthly, by `at`) and
// idempotency_keys (daily, by `expires_at`) — have the shape the rest of the
// system assumes, and that PartitionMaintainer actually populates them.
//
// This used to test the pre-baseline rewrite migrations that converted these
// tables in place. Those migrations no longer exist: the consolidated
// baseline creates both tables partitioned from the start, so there is no
// copy to verify. What still needs verifying is everything the rewrite was
// at risk of losing — the partitioning itself, the indexes, RLS, and the
// tenant FK — plus the live mechanism that creates partitions, which on a
// fresh database is the maintainer rather than a migration.
package components

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

func TestPartitionedTablesShape(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)

	tenant := "11111111-1111-1111-1111-111111111111"
	mustExec(t, ctx, pool,
		`INSERT INTO tenants (id, display_name, slug) VALUES ($1, 'Test', 't-part')`,
		tenant)

	// Both tables are partitioned parents.
	assertCount(ctx, t, pool,
		`SELECT count(*) FROM pg_partitioned_table p JOIN pg_class c ON c.oid = p.partrelid
		 WHERE c.relname IN ('audit_log','idempotency_keys')`, 2)

	// Indexes live on the partitioned parent, so they propagate to every
	// partition the maintainer creates later. These are the ones easiest to
	// lose when the table is rebuilt by hand.
	assertCount(ctx, t, pool,
		`SELECT count(*) FROM pg_indexes WHERE tablename = 'audit_log'
		 AND indexname IN ('idx_audit_log_tenant_at','idx_audit_log_action_at','idx_audit_log_capability')`, 3)
	assertCount(ctx, t, pool,
		`SELECT count(*) FROM pg_indexes WHERE tablename = 'idempotency_keys'
		 AND indexname = 'idx_idempotency_keys_expiry'`, 1)

	// RLS is a primary isolation control on audit_log; a partitioned parent
	// enforces it for every partition, so losing it here loses it everywhere.
	assertCount(ctx, t, pool,
		`SELECT count(*) FROM pg_class WHERE relname='audit_log' AND relrowsecurity AND relforcerowsecurity`, 1)
	assertCount(ctx, t, pool,
		`SELECT count(*) FROM pg_policies WHERE tablename='audit_log' AND policyname='tenant_write_isolation'`, 1)

	// The tenant FK survives partitioning — Postgres allows it on a
	// partitioned table, and dropping it would orphan idempotency records
	// when a tenant is deleted.
	assertCount(ctx, t, pool,
		`SELECT count(*) FROM pg_constraint WHERE conname='idempotency_keys_tenant_id_fkey'
		 AND conrelid='idempotency_keys'::regclass AND contype='f'`, 1)
}

// TestPartitionMaintainerRoutesOutOfDefault covers the recovery path that
// makes a fresh deployment safe: writes that land before their partition
// exists pile into DEFAULT, and DEFAULT is reclaimed by DELETE rather than
// DROP. The maintainer has to relocate them, not just create partitions
// going forward — otherwise the first month of every deployment is
// permanently stuck in the catch-all.
func TestPartitionMaintainerRoutesOutOfDefault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)

	tenant := "22222222-2222-2222-2222-222222222222"
	mustExec(t, ctx, pool,
		`INSERT INTO tenants (id, display_name, slug) VALUES ($1, 'Test', 't-part2')`,
		tenant)

	now := time.Now().UTC()
	// Seeded before any partition exists, so all three land in DEFAULT.
	// The NULL actor_tenant_id row is the one RLS accepts but a naive
	// relocation copy would drop.
	for _, r := range []struct {
		at     time.Time
		tenant any
	}{{now, tenant}, {now, tenant}, {now, nil}} {
		mustExec(t, ctx, pool,
			`INSERT INTO audit_log (id, at, actor_subject, actor_tenant_id,
			   actor_audience, action, resource_name)
			 VALUES (gen_random_uuid(), $1, 'tester', $2, 'admin', 'admin.X.Do', 'res')`,
			r.at, r.tenant)
	}
	mustExec(t, ctx, pool,
		`INSERT INTO idempotency_keys (tenant_id, method, key, response, response_sha, expires_at)
		 VALUES ($1, 'POST', 'ka', '\x00', '\x00', $2)`, tenant, now.Add(time.Hour))

	assertCount(ctx, t, pool,
		`SELECT count(*) FROM audit_log WHERE tableoid::regclass::text = 'audit_log_default'`, 3)

	m := &worker.PartitionMaintainer{
		DB: pool,
		Specs: []worker.PartitionSpec{
			{Table: "audit_log", Period: worker.PeriodMonthly, Ahead: 3, PartitionKey: "at"},
			{Table: "idempotency_keys", Period: worker.PeriodDaily, Ahead: 8, PartitionKey: "expires_at"},
		},
		Logger: zap.NewNop(),
	}
	m.RunOnce(ctx)

	// Every row moved into a real partition, none was lost, and the NULL
	// tenant survived the move.
	assertCount(ctx, t, pool, `SELECT count(*) FROM audit_log`, 3)
	assertCount(ctx, t, pool, `SELECT count(*) FROM audit_log WHERE actor_tenant_id IS NULL`, 1)
	assertCount(ctx, t, pool,
		`SELECT count(*) FROM audit_log WHERE tableoid::regclass::text = 'audit_log_default'`, 0)
	assertCount(ctx, t, pool, `SELECT count(*) FROM idempotency_keys`, 1)
	assertCount(ctx, t, pool,
		`SELECT count(*) FROM idempotency_keys
		  WHERE tableoid::regclass::text = 'idempotency_keys_default'`, 0)

	// Writes after the maintainer ran route straight into a partition.
	mustExec(t, ctx, pool,
		`INSERT INTO audit_log (id, at, actor_subject, actor_tenant_id,
		   actor_audience, action, resource_name)
		 VALUES (gen_random_uuid(), now(), 'post', $1, 'admin', 'admin.X.Do', 'res')`,
		tenant)
	assertCount(ctx, t, pool, `SELECT count(*) FROM audit_log`, 4)
	assertCount(ctx, t, pool,
		`SELECT count(*) FROM audit_log WHERE tableoid::regclass::text = 'audit_log_default'`, 0)
}

func assertCount(ctx context.Context, t *testing.T, pool *pgxpool.Pool, q string, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(ctx, q).Scan(&got); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	if got != want {
		t.Errorf("query %q = %d, want %d", q, got, want)
	}
}
