//go:build integration

package components

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/oleg-tkachuk/paladin/backend/migrations"
)

// One Postgres for the package, and a database per test.
//
// Every test used to start its own container and run every migration in it:
// about 3.2s each, over a hundred times, some 60% of the package's 572s. Now
// the first startPostgres starts one container and migrates a template
// database; each call clones it — CREATE DATABASE ... TEMPLATE takes a
// fraction of a second — and drops the clone when the test ends.
//
// What a test could rely on is unchanged: its own database, migrated, with no
// rows from any other test. Tables, sequences, LISTEN/NOTIFY channels and
// advisory locks are all per database. Roles are not — they belong to the
// server — but the tests run one at a time and every role a test alters is
// set to the state the migrations give it.
const (
	postgresImage = "postgres:17-alpine"
	postgresUser  = "paladin"
	// The database tests are cloned from. Nothing stays connected to it:
	// CREATE DATABASE ... TEMPLATE refuses a template that has a session.
	templateDB = "paladin_template"
	// Where the harness itself connects to create and drop the clones.
	maintenanceDB = "postgres"
	// testcontainers' readiness probe: Postgres logs this twice, once for
	// the init-time server and once for the real one.
	postgresReadyLog       = "database system is ready to accept connections"
	postgresReadyLogCount  = 2
	postgresStartupTimeout = 90 * time.Second
	// The prefix of every per-test database; a counter makes each unique for
	// the life of the test binary, -count=N included.
	testDBPrefix = "t_"
)

var shared struct {
	once        sync.Once
	err         error
	container   *tcpostgres.PostgresContainer
	maintenance *pgxpool.Pool
	// base is the connection config the per-test pools copy, with their own
	// database substituted.
	base *pgx.ConnConfig
	next atomic.Uint64
}

// TestMain stops the shared container once the package's tests are done. It
// starts nothing: the container comes up on the first startPostgres, so a run
// that selects no Postgres test never touches Docker.
func TestMain(m *testing.M) {
	code := m.Run()
	if shared.maintenance != nil {
		shared.maintenance.Close()
	}
	if shared.container != nil {
		_ = shared.container.Terminate(context.Background())
	}
	os.Exit(code)
}

func startShared() error {
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase(templateDB),
		tcpostgres.WithUsername(postgresUser),
		tcpostgres.WithPassword(postgresUser),
		testcontainers.WithWaitStrategy(
			wait.ForLog(postgresReadyLog).
				WithOccurrence(postgresReadyLogCount).
				WithStartupTimeout(postgresStartupTimeout)),
	)
	if err != nil {
		return fmt.Errorf("start postgres container: %w", err)
	}
	shared.container = ctr

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return fmt.Errorf("connection string: %w", err)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse dsn: %w", err)
	}
	shared.base = cfg

	// Migrate the template through the same goose + embedded FS the app uses,
	// then close the connection so the template can be cloned.
	sqlDB := stdlib.OpenDB(*cfg)
	if err := goose.SetDialect("postgres"); err != nil {
		_ = sqlDB.Close()
		return fmt.Errorf("goose dialect: %w", err)
	}
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.Up(sqlDB, "."); err != nil {
		_ = sqlDB.Close()
		return fmt.Errorf("goose up: %w", err)
	}
	if err := sqlDB.Close(); err != nil {
		return fmt.Errorf("close template connection: %w", err)
	}

	shared.maintenance, err = poolOn(ctx, maintenanceDB)
	if err != nil {
		return fmt.Errorf("maintenance pool: %w", err)
	}
	return nil
}

// poolOn opens a pool on the named database of the shared server.
func poolOn(ctx context.Context, db string) (*pgxpool.Pool, error) {
	pcfg, err := pgxpool.ParseConfig("")
	if err != nil {
		return nil, fmt.Errorf("pool config: %w", err)
	}
	conn := shared.base.Copy()
	conn.Database = db
	pcfg.ConnConfig = conn
	return pgxpool.NewWithConfig(ctx, pcfg)
}

// startPostgres returns a pool on a fresh, fully migrated database of this
// test's own, and drops the database when the test ends.
func startPostgres(tb testing.TB) *pgxpool.Pool {
	tb.Helper()
	shared.once.Do(func() { shared.err = startShared() })
	if shared.err != nil {
		tb.Fatalf("shared postgres: %v", shared.err)
	}
	ctx := context.Background()

	name := fmt.Sprintf("%s%d", testDBPrefix, shared.next.Add(1))
	ident := pgx.Identifier{name}.Sanitize()
	if _, err := shared.maintenance.Exec(ctx,
		"CREATE DATABASE "+ident+" TEMPLATE "+pgx.Identifier{templateDB}.Sanitize()); err != nil {
		tb.Fatalf("clone %s: %v", templateDB, err)
	}
	// Registered before the pool's Close, so it runs after it (cleanups are
	// LIFO). FORCE ends the sessions a test's background workers still hold.
	tb.Cleanup(func() {
		if _, err := shared.maintenance.Exec(context.Background(),
			"DROP DATABASE IF EXISTS "+ident+" WITH (FORCE)"); err != nil {
			tb.Errorf("drop %s: %v", name, err)
		}
	})

	pool, err := poolOn(ctx, name)
	if err != nil {
		tb.Fatalf("open pool: %v", err)
	}
	tb.Cleanup(pool.Close)
	return pool
}
