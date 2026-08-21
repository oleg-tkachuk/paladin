//go:build integration

// Package pgharness spins up a Postgres container for integration
// tests, applies the embedded migrations, and hands back the pool
// connections plus role DSNs the tests need.
//
// Cost: container start ≈ 2-3s on a warm cache, ≈ 8-10s cold. The
// `integration` build tag keeps it out of the default `go test ./...`
// path.
//
// Lifecycle: Setup() returns a *Harness with t.Cleanup wired so the
// container terminates when the test (or the parent test in a
// subtests-with-shared-harness setup) finishes. Tests do NOT call
// Close manually.
//
// Two pool roles are exposed:
//
//   - PoolMigrate (BYPASSRLS via paladin_migrate role): lets tests
//     insert seed data across tenants without RLS getting in the
//     way. The same role goose uses to apply DDL.
//
//   - PoolApp (RLS-enforced via paladin_app + WithRLS hooks): the
//     production-shaped pool. Tests that exercise tenant
//     isolation use this one and stamp tenant via auth context.
//
//   - PoolAppNoGUC (paladin_app, NO RLS hook, tenant GUC never set): the
//     production FAILURE mode a cross-tenant background component hits
//     when it runs on the RLS runtime pool with no request principal —
//     `paladin_session_tenant_id()` is NULL, so every RLS policy matches
//     zero rows. This is the exact condition the reaper + dispatcher
//     bugs hit; AssertRLSHidesCrossTenant asserts it.
package pgharness

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for goose
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/oleg-tkachuk/paladin/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/migrations"
)

// Harness is the per-test (or per-suite) Postgres environment.
type Harness struct {
	Container testcontainers.Container

	// PoolMigrate runs as `paladin_migrate` (BYPASSRLS). Use for seeding
	// state across tenants in test setup.
	PoolMigrate *pgxpool.Pool

	// PoolApp runs as `paladin_app` with the RLS BeforeAcquire hook
	// installed. Tests use this to exercise tenant-isolation
	// behaviour the production runtime sees.
	PoolApp *pgxpool.Pool

	// PoolAppNoGUC runs as `paladin_app` with NO RLS hook — the tenant GUC is
	// never set, so paladin_session_tenant_id() is NULL and every RLS policy
	// matches zero rows. This reproduces the cross-tenant-background-job
	// failure mode (reaper / dispatcher on the runtime pool with no request
	// principal). Use AssertRLSHidesCrossTenant.
	PoolAppNoGUC *pgxpool.Pool

	// DSN strings for either role, in case a test needs to open its
	// own connection (e.g. to verify connection-time GUC behaviour).
	MigrateDSN string
	AppDSN     string
}

// Setup launches Postgres, applies migrations, creates the
// `paladin_app` runtime role, and returns the harness. Failures here
// fail the test up-front (t.Fatalf).
func Setup(t *testing.T) *Harness {
	t.Helper()
	ctx := context.Background()

	pgC, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("paladin"),
		tcpostgres.WithUsername("paladin_migrate"),
		tcpostgres.WithPassword("paladin"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("postgres container start: %v", err)
	}
	t.Cleanup(func() {
		// Use a fresh ctx — the test ctx may already be cancelled.
		_ = pgC.Terminate(context.Background())
	})

	// Tier the migrate role up to BYPASSRLS so the RLS baseline (002_roles_and_rls.sql) can
	// re-set the same property idempotently. testcontainers/postgres
	// creates the configured user without it.
	migrateDSN, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	if err := preconfigureMigrateRole(ctx, migrateDSN); err != nil {
		t.Fatalf("preconfigure migrate role: %v", err)
	}

	// Run all migrations. Migration 011 creates paladin_app NOLOGIN; we
	// promote it post-migration so the test pool can connect.
	if err := applyMigrations(migrateDSN); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	if err := promoteAppRole(ctx, migrateDSN); err != nil {
		t.Fatalf("promote app role: %v", err)
	}

	// Build the migrate-role pool (BYPASSRLS). Used for seed data.
	poolMigrate, err := pgxpool.New(ctx, migrateDSN)
	if err != nil {
		t.Fatalf("migrate pool: %v", err)
	}
	t.Cleanup(poolMigrate.Close)

	// Build the app-role pool with the RLS BeforeAcquire hook.
	host, _ := pgC.Host(ctx)
	port, _ := pgC.MappedPort(ctx, "5432/tcp")
	appDSN := fmt.Sprintf("postgres://paladin_app:paladin_app@%s:%s/paladin?sslmode=disable", host, port.Port())

	appCfg, err := pgxpool.ParseConfig(appDSN)
	if err != nil {
		t.Fatalf("app pool parse: %v", err)
	}
	postgres.EnableRLS(appCfg)
	poolApp, err := pgxpool.NewWithConfig(ctx, appCfg)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(poolApp.Close)

	// paladin_app pool WITHOUT the RLS hook — the tenant GUC is never set, so RLS
	// policies match nothing cross-tenant. Reproduces the background-job bug.
	poolAppNoGUC, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("app pool (no GUC): %v", err)
	}
	t.Cleanup(poolAppNoGUC.Close)

	return &Harness{
		Container:    pgC,
		PoolMigrate:  poolMigrate,
		PoolApp:      poolApp,
		PoolAppNoGUC: poolAppNoGUC,
		MigrateDSN:   migrateDSN,
		AppDSN:       appDSN,
	}
}

// AssertRLSHidesCrossTenant proves the production failure mode the reaper +
// dispatcher bugs hit: a cross-tenant read on a GUC-less paladin_app pool returns
// ZERO rows (RLS matches nothing without a tenant GUC), while the same read on
// the BYPASSRLS migrate pool sees them. countSQL must be a
// `SELECT count(*) FROM <rls_table> ...` with NO tenant predicate; call it
// after seeding the rows via PoolMigrate. A cross-tenant background component
// MUST run on a BYPASSRLS pool — this is the shared assertion that pins it.
func (h *Harness) AssertRLSHidesCrossTenant(t *testing.T, countSQL string, args ...any) {
	t.Helper()
	ctx := context.Background()

	var appN, migN int64
	if err := h.PoolAppNoGUC.QueryRow(ctx, countSQL, args...).Scan(&appN); err != nil {
		t.Fatalf("AssertRLSHidesCrossTenant: GUC-less app pool query: %v", err)
	}
	if err := h.PoolMigrate.QueryRow(ctx, countSQL, args...).Scan(&migN); err != nil {
		t.Fatalf("AssertRLSHidesCrossTenant: migrate pool query: %v", err)
	}
	if migN == 0 {
		t.Fatalf("AssertRLSHidesCrossTenant: BYPASSRLS pool saw 0 rows — seed rows via PoolMigrate first (nothing to assert)")
	}
	if appN != 0 {
		t.Errorf("AssertRLSHidesCrossTenant: GUC-less paladin_app saw %d rows, want 0 — a cross-tenant background component on the RLS pool would silently no-op; it MUST use a BYPASSRLS pool", appN)
	}
}

// preconfigureMigrateRole grants BYPASSRLS to paladin_migrate so RLS
// migrations don't gate seeding. Idempotent.
func preconfigureMigrateRole(ctx context.Context, dsn string) error {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, `ALTER ROLE paladin_migrate BYPASSRLS`)
	return err
}

// promoteAppRole sets a password on paladin_app and gives it LOGIN so
// the test pool can connect. Migration 011 creates the role NOLOGIN
// (production: DBA sets the password out-of-band); the harness
// provides one inline.
func promoteAppRole(ctx context.Context, dsn string) error {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, `ALTER ROLE paladin_app WITH LOGIN PASSWORD 'paladin_app'`)
	return err
}

// applyMigrations runs goose against the embedded migration FS. Uses
// database/sql + the pgx stdlib driver because goose's API is sql.DB-
// shaped. Closes its own connection.
func applyMigrations(dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	goose.SetBaseFS(migrations.FS)
	return goose.Up(db, ".")
}
