//go:build integration

// Regression guard for the FR-008 idempotency-replay bug.
//
// Migration 042 range-partitioned idempotency_keys on expires_at, which
// widened the primary key to (tenant_id, method, key, expires_at). But
// PutIdempotencyKey's ON CONFLICT still targeted the old 3-column
// (tenant_id, method, key) tuple — no longer a unique constraint on a
// partitioned table — so every write raised 42P10 ("no unique or exclusion
// constraint matching the ON CONFLICT"). The idempotency interceptor
// swallows Put errors, so memoization silently never happened and admin
// double-submits (CapabilityService.Issue, CreateTenant, …) duplicated
// resources.
//
// A memStore unit test cannot catch this — the failure lives in the SQL
// against the real partitioned schema. This drives the PRODUCTION adapter
// (the exact code path the interceptor uses) against a fully-migrated
// Postgres and proves Put lands and a subsequent Get replays it.
package integration

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/migrations"
)

func TestIdempotencyReplayAgainstPartitionedTable(t *testing.T) {
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
	// 023/024 re-set BYPASSRLS on paladin_migrate; grant it up front so the RLS
	// migrations apply and the tenant seed is not gated.
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
	// Apply EVERY migration — including 042's partitioning — so the schema
	// matches production exactly.
	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	tenant := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	if _, err := pool.Exec(ctx,
		`INSERT INTO tenants (tenant_id, display_name, slug) VALUES ($1, 'Test', 't-idem')`,
		tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	repo := adapters.NewIdempotencyRepo(sqlc.New(pool))
	const method = "/paladin.admin.v1.CapabilityService/Issue"
	key := uuid.NewString()
	resp := []byte("capability-response-bytes")
	sha := []byte("sha-32-bytes-placeholder--------")
	exp := time.Now().Add(time.Hour)

	// First write. Before the fix this raised 42P10 and the row never landed.
	if err := repo.Put(ctx, tenant, method, key, resp, sha, exp); err != nil {
		t.Fatalf("Put must succeed against the partitioned table; got %v", err)
	}

	// The second submit (same Idempotency-Key) must read the memoized
	// response — this is the replay that collapses a double-submit.
	got, gotSha, found, err := repo.Get(ctx, tenant, method, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found {
		t.Fatal("memoized response must be found on replay — Put silently failed (the FR-008 bug)")
	}
	if string(got) != string(resp) || string(gotSha) != string(sha) {
		t.Fatalf("replayed payload mismatch: resp=%q sha=%q", got, gotSha)
	}

	// A racing first-writer (same key, same expiry) must be a no-op, not an
	// error — DO NOTHING on the full-PK conflict.
	if err := repo.Put(ctx, tenant, method, key, resp, sha, exp); err != nil {
		t.Fatalf("repeat Put must be a no-op, not an error; got %v", err)
	}
}
