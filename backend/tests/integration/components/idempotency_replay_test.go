//go:build integration

// Regression guard for the FR-008 idempotency-replay bug.
//
// Range-partitioning idempotency_keys on expires_at (now in
// `001_initial_schema.sql`) widened its unique key to
// (tenant_id, method, key, expires_at). But
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
package components

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

	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/migrations"
)

func TestIdempotencyReplayAgainstPartitionedTable(t *testing.T) {
	// Not parallel: sets goose's package-level dialect and filesystem.
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
	// Apply EVERY migration — including the baseline's partitioning — so the schema
	// matches production exactly.
	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	tenant := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	if _, err := pool.Exec(ctx,
		`INSERT INTO tenants (id, display_name, slug) VALUES ($1, 'Test', 't-idem')`,
		tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	repo := adapters.NewIdempotencyRepo(sqlc.New(pool))
	const method = "/paladin.admin.v1.CapabilityService/Issue"
	key := uuid.NewString()
	rec := middleware.IdempotencyRecord{
		Response:    []byte("capability-response-bytes"),
		RequestHash: []byte("request-fingerprint-32-bytes----"),
	}
	exp := time.Now().Add(time.Hour)

	// First write. Before the fix this raised 42P10 and the row never landed.
	if err := repo.Put(ctx, tenant, method, key, rec, exp); err != nil {
		t.Fatalf("Put must succeed against the partitioned table; got %v", err)
	}

	// The second submit (same Idempotency-Key) must read the memoized
	// response — this is the replay that collapses a double-submit — and the
	// fingerprint the interceptor compares before replaying.
	got, found, err := repo.Get(ctx, tenant, method, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found {
		t.Fatal("memoized response must be found on replay — Put silently failed (the FR-008 bug)")
	}
	if string(got.Response) != string(rec.Response) || string(got.RequestHash) != string(rec.RequestHash) {
		t.Fatalf("replayed record mismatch: response=%q request_hash=%q", got.Response, got.RequestHash)
	}

	// A racing first-writer (same key, same expiry) must be a no-op, not an
	// error — DO NOTHING on the full-PK conflict.
	if err := repo.Put(ctx, tenant, method, key, rec, exp); err != nil {
		t.Fatalf("repeat Put must be a no-op, not an error; got %v", err)
	}

	// A row written before request_hash existed reads back with no
	// fingerprint, which the interceptor treats as matching any request.
	legacyKey := uuid.NewString()
	if _, err := pool.Exec(ctx,
		`INSERT INTO idempotency_keys (tenant_id, method, key, response, response_sha, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		tenant, method, legacyKey, rec.Response, []byte("sha"), exp); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	legacy, found, err := repo.Get(ctx, tenant, method, legacyKey)
	if err != nil || !found {
		t.Fatalf("legacy Get = (found %v, %v)", found, err)
	}
	if len(legacy.RequestHash) != 0 {
		t.Errorf("legacy row request_hash = %x, want empty", legacy.RequestHash)
	}
}
