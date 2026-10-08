//go:build integration

// Package pgharness gives each integration test its own migrated Postgres
// database, and hands back the pool connections plus role DSNs the tests
// need.
//
// One container serves the whole test binary. The first Setup starts it,
// migrates a template database and configures the roles; every Setup then
// clones the template (CREATE DATABASE ... TEMPLATE, a fraction of a second
// against the 2-10s a container start costs) and drops the clone when the
// test ends. Tables, sequences, LISTEN/NOTIFY channels and advisory locks are
// per database, so a test sees nothing another wrote. Roles are server-wide:
// the harness sets them up once, and a test must not alter them.
//
// Lifecycle: Setup() returns a *Harness with t.Cleanup wired, so tests do
// NOT close anything. A package's TestMain calls Main so the container stops
// with the binary; without it, the testcontainers reaper removes it.
//
// The `integration` build tag keeps it out of the default `go test ./...`.
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
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/pgtest"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for goose
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/backend/migrations"
)

const (
	postgresImage = pgtest.Image
	// migrateRole is the container's bootstrap superuser, and the role goose
	// applies DDL as.
	migrateRole     = "paladin_migrate"
	migratePassword = "paladin"
	// appRole is created NOLOGIN by the migrations; promoteAppRole gives it
	// this password.
	appRole     = "paladin_app"
	appPassword = "paladin_app"
	// templateDB is migrated once and cloned per test. Nothing stays
	// connected to it: CREATE DATABASE ... TEMPLATE refuses a template that
	// has a session.
	templateDB = "paladin_template"
	// maintenanceDB is where the clones are created and dropped from.
	maintenanceDB = "postgres"
	cloneDBPrefix = "t_"

	readyLog       = "database system is ready to accept connections"
	readyLogCount  = 2 // once for the init run, once for the real server
	startupTimeout = 60 * time.Second
	sslDisabled    = "sslmode=disable"
)

// Harness is one test's Postgres environment: a database of its own on the
// binary's shared server.
type Harness struct {
	// Container is the shared server. Stopping it stops every test's
	// database, so a test must not.
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

	// DSN strings for either role on this test's database, in case a test
	// needs to open its own connection (e.g. to verify connection-time GUC
	// behaviour).
	MigrateDSN string
	AppDSN     string
}

var shared struct {
	once        sync.Once
	err         error
	container   *tcpostgres.PostgresContainer
	maintenance *pgxpool.Pool
	// dsn is the superuser DSN of the template; per-test DSNs swap the
	// database and, for the app role, the user.
	dsn  *url.URL
	next atomic.Uint64
}

// Main runs the package's tests and stops the shared container afterwards.
// Call it from TestMain: os.Exit(pgharness.Main(m)).
func Main(m *testing.M) int {
	code := m.Run()
	if shared.maintenance != nil {
		shared.maintenance.Close()
	}
	if shared.container != nil {
		_ = shared.container.Terminate(context.Background())
	}
	return code
}

// Setup clones the migrated template into a database of this test's own and
// returns pools on it. Failures here fail the test up-front (t.Fatalf).
func Setup(t *testing.T) *Harness {
	t.Helper()
	shared.once.Do(func() { shared.err = startShared() })
	if shared.err != nil {
		t.Fatalf("shared postgres: %v", shared.err)
	}
	ctx := context.Background()

	name := fmt.Sprintf("%s%d", cloneDBPrefix, shared.next.Add(1))
	ident := pgx.Identifier{name}.Sanitize()
	if _, err := shared.maintenance.Exec(ctx,
		"CREATE DATABASE "+ident+" TEMPLATE "+pgx.Identifier{templateDB}.Sanitize()); err != nil {
		t.Fatalf("clone %s: %v", templateDB, err)
	}
	// Registered before the pools' Close, so it runs after them (cleanups
	// are LIFO). FORCE ends sessions a test's own components still hold.
	t.Cleanup(func() {
		if _, err := shared.maintenance.Exec(context.Background(),
			"DROP DATABASE IF EXISTS "+ident+" WITH (FORCE)"); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
	})

	migrateDSN := dsnFor(name, url.UserPassword(migrateRole, migratePassword))
	appDSN := dsnFor(name, url.UserPassword(appRole, appPassword))

	// Build the migrate-role pool (BYPASSRLS). Used for seed data.
	poolMigrate, err := pgxpool.New(ctx, migrateDSN)
	if err != nil {
		t.Fatalf("migrate pool: %v", err)
	}
	t.Cleanup(poolMigrate.Close)

	// Build the app-role pool with the RLS BeforeAcquire hook.
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
		Container:    shared.container,
		PoolMigrate:  poolMigrate,
		PoolApp:      poolApp,
		PoolAppNoGUC: poolAppNoGUC,
		MigrateDSN:   migrateDSN,
		AppDSN:       appDSN,
	}
}

// startShared launches the server, configures the roles and migrates the
// template. Runs once per test binary.
func startShared() error {
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase(templateDB),
		tcpostgres.WithUsername(migrateRole),
		tcpostgres.WithPassword(migratePassword),
		testcontainers.WithWaitStrategy(
			wait.ForLog(readyLog).
				WithOccurrence(readyLogCount).
				WithStartupTimeout(startupTimeout),
		),
	)
	if err != nil {
		return fmt.Errorf("postgres container start: %w", err)
	}
	shared.container = ctr

	raw, err := ctr.ConnectionString(ctx, sslDisabled)
	if err != nil {
		return fmt.Errorf("connection string: %w", err)
	}
	if shared.dsn, err = url.Parse(raw); err != nil {
		return fmt.Errorf("parse connection string: %w", err)
	}

	// Tier the migrate role up to BYPASSRLS so the RLS baseline
	// (002_roles_and_rls.sql) can re-set the same property idempotently.
	// testcontainers/postgres creates the configured user without it.
	if err := preconfigureMigrateRole(ctx, raw); err != nil {
		return fmt.Errorf("preconfigure migrate role: %w", err)
	}
	// Run all migrations. The migrations create paladin_app NOLOGIN; it is
	// promoted afterwards so the test pools can connect.
	if err := applyMigrations(raw); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	if err := promoteAppRole(ctx, raw); err != nil {
		return fmt.Errorf("promote app role: %w", err)
	}

	shared.maintenance, err = pgxpool.New(ctx, dsnFor(maintenanceDB, shared.dsn.User))
	if err != nil {
		return fmt.Errorf("maintenance pool: %w", err)
	}
	return nil
}

// dsnFor is the shared server's DSN for database db as user.
func dsnFor(db string, user *url.Userinfo) string {
	u := *shared.dsn
	u.User = user
	u.Path = "/" + db
	return u.String()
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
	_, err = pool.Exec(ctx, "ALTER ROLE "+migrateRole+" BYPASSRLS")
	return err
}

// promoteAppRole sets a password on paladin_app and gives it LOGIN so
// the test pool can connect. `002_roles_and_rls.sql` creates the role NOLOGIN
// (production: DBA sets the password out-of-band); the harness
// provides one inline.
func promoteAppRole(ctx context.Context, dsn string) error {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, "ALTER ROLE "+appRole+" WITH LOGIN PASSWORD '"+appPassword+"'")
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
