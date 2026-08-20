//go:build integration

// Verifies the partitioning rewrites (migrations 041 audit_log, 042
// idempotency_keys) the way a real maintenance-window cutover would: seed
// rows into the pre-partition tables (schema at version 040), apply the
// rewrites, and assert the data survived the copy, routes into real
// partitions, keeps its indexes, and (audit_log) keeps RLS.
//
// This is the check that hand-written rewrite SQL most needs — the data
// copy and the post-rewrite shape — and the one a fresh-DB suite cannot
// give, because on a fresh DB the copy is a no-op.
package integration

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/oleg-tkachuk/paladin/migrations"
)

// prePartitionVersion is the last migration before the partitioning pair.
// Seeding happens at this version, then Up applies 041 + 042.
const prePartitionVersion = 40

func TestPartitionRewrite(t *testing.T) {
	ctx := context.Background()

	pgC, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("paladin"),
		tcpostgres.WithUsername("paladin_migrate"),
		tcpostgres.WithPassword("paladin"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("postgres start: %v", err)
	}
	t.Cleanup(func() { _ = pgC.Terminate(context.Background()) })

	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	// Migration 023/024 re-set BYPASSRLS on paladin_migrate; grant it up front so
	// the RLS migrations apply and seeding is not gated.
	if _, err := pool.Exec(ctx, `ALTER ROLE paladin_migrate BYPASSRLS`); err != nil {
		t.Fatalf("bypassrls: %v", err)
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	goose.SetBaseFS(migrations.FS)

	// --- Apply through 040, then seed the pre-partition tables. ---
	if err := goose.UpTo(db, ".", prePartitionVersion); err != nil {
		t.Fatalf("up to %d: %v", prePartitionVersion, err)
	}

	twoMonthsAgo := time.Now().UTC().AddDate(0, -2, 0)
	now := time.Now().UTC()
	tenant := "11111111-1111-1111-1111-111111111111"

	// idempotency_keys.tenant_id has an ON DELETE CASCADE FK to tenants
	// (migration 004); the row must exist before seeding and the rewrite
	// must preserve the FK. slug obeys the kebab format check (migration 009).
	if _, err := pool.Exec(ctx,
		`INSERT INTO tenants (id, display_name, slug) VALUES ($1, 'Test', 't-part')`,
		tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	// 3 audit rows: an old month (routes to a past monthly partition), a
	// current-month row, and a current row with NULL actor_tenant_id (RLS
	// accepts NULL; copy must preserve it).
	for i, r := range []struct {
		at     time.Time
		tenant any
	}{
		{twoMonthsAgo, tenant},
		{now, tenant},
		{now, nil},
	} {
		_, err := pool.Exec(ctx,
			`INSERT INTO audit_log (id, at, actor_subject, actor_tenant_id,
			   actor_audience, action, resource_name)
			 VALUES (gen_random_uuid(), $1, 'tester', $2, 'admin', 'admin.X.Do', 'res')`,
			r.at, r.tenant)
		if err != nil {
			t.Fatalf("seed audit row %d: %v", i, err)
		}
	}

	// 2 idempotency rows: one already expired (routes near today), one with a
	// forward TTL.
	for i, exp := range []time.Time{now.Add(-1 * time.Hour), now.Add(24 * time.Hour)} {
		_, err := pool.Exec(ctx,
			`INSERT INTO idempotency_keys (tenant_id, method, key, response, response_sha, expires_at)
			 VALUES ($1, 'POST', $2, '\x00', '\x00', $3)`,
			tenant, "k"+string(rune('a'+i)), exp)
		if err != nil {
			t.Fatalf("seed idempotency row %d: %v", i, err)
		}
	}

	// --- Apply the partitioning rewrites. ---
	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("apply partition migrations: %v", err)
	}

	// 1. Data survived the copy.
	assertCount(ctx, t, pool, "SELECT count(*) FROM audit_log", 3)
	assertCount(ctx, t, pool, "SELECT count(*) FROM idempotency_keys", 2)
	// NULL actor_tenant_id preserved.
	assertCount(ctx, t, pool, "SELECT count(*) FROM audit_log WHERE actor_tenant_id IS NULL", 1)

	// 2. Both tables are now partitioned.
	assertCount(ctx, t, pool,
		`SELECT count(*) FROM pg_partitioned_table p JOIN pg_class c ON c.oid = p.partrelid
		 WHERE c.relname IN ('audit_log','idempotency_keys')`, 2)

	// 3. The old-month audit row landed in a real monthly partition, not the
	// default catch-all.
	var part string
	if err := pool.QueryRow(ctx,
		`SELECT tableoid::regclass::text FROM audit_log WHERE at = $1`, twoMonthsAgo).
		Scan(&part); err != nil {
		t.Fatalf("partition lookup: %v", err)
	}
	if part == "audit_log_default" || part == "audit_log" {
		t.Errorf("old-month row landed in %q, want a monthly partition", part)
	}

	// 4. Indexes propagated to the partitioned parent (sample the two that
	// are easy to lose in a hand-written rewrite: the partial and the
	// text_pattern_ops one).
	assertCount(ctx, t, pool,
		`SELECT count(*) FROM pg_indexes WHERE tablename = 'audit_log'
		 AND indexname IN ('idx_audit_log_tenant_at','idx_audit_log_action_at','idx_audit_log_capability')`, 3)
	assertCount(ctx, t, pool,
		`SELECT count(*) FROM pg_indexes WHERE tablename = 'idempotency_keys'
		 AND indexname = 'idx_idempotency_keys_expiry'`, 1)

	// 5. RLS survived on audit_log (relrowsecurity + the policy).
	assertCount(ctx, t, pool,
		`SELECT count(*) FROM pg_class WHERE relname='audit_log' AND relrowsecurity AND relforcerowsecurity`, 1)
	assertCount(ctx, t, pool,
		`SELECT count(*) FROM pg_policies WHERE tablename='audit_log' AND policyname='tenant_write_isolation'`, 1)

	// 6. The tenant FK survived the idempotency_keys rewrite.
	assertCount(ctx, t, pool,
		`SELECT count(*) FROM pg_constraint WHERE conname='idempotency_keys_tenant_id_fkey'
		 AND conrelid='idempotency_keys'::regclass AND contype='f'`, 1)

	// 7. New writes route through the parent and are queryable.
	if _, err := pool.Exec(ctx,
		`INSERT INTO audit_log (id, at, actor_subject, actor_tenant_id,
		   actor_audience, action, resource_name)
		 VALUES (gen_random_uuid(), now(), 'post', $1, 'admin', 'admin.X.Do', 'res')`,
		tenant); err != nil {
		t.Fatalf("post-rewrite insert: %v", err)
	}
	assertCount(ctx, t, pool, "SELECT count(*) FROM audit_log", 4)
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
