//go:build integration

// Real-Postgres regression test for the lease claim SQL. The unit tests
// (lease_test.go / renewer_test.go) drive the renewer through a FAKE claimFunc
// and never execute the actual INSERT … ON CONFLICT … RETURNING — which is how
// a truncated statement ("RETURNING generation," + a 6-value VALUES for 7
// columns) shipped in the monorepo restructure and silently killed every
// leased worker until a real DB ran it. This test runs the real SQL.
//
// Gated behind `integration` (needs Docker/testcontainers):
//
//	go test -tags=integration ./internal/worker/lease/...
package lease

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin-private/migrations"
)

func startPostgres(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("paladin"),
		tcpostgres.WithUsername("paladin"),
		tcpostgres.WithPassword("paladin"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(90*time.Second)),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	cc, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	sqlDB := stdlib.OpenDB(*cc)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose dialect: %v", err)
	}
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.Up(sqlDB, "."); err != nil {
		t.Fatalf("goose up: %v", err)
	}
	_ = sqlDB.Close()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newLease(t *testing.T, pool *pgxpool.Pool, name string, holder uuid.UUID) *Lease {
	t.Helper()
	l, err := New(pool, Config{
		Name:       name,
		HolderID:   holder,
		HolderMeta: map[string]string{"host": "test"},
		TTL:        time.Minute,
		Logger:     zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("New lease: %v", err)
	}
	return l
}

func TestClaim_RealSQL_AcquireRenewContend(t *testing.T) {
	pool := startPostgres(t)
	ctx := context.Background()

	holderA := newLease(t, pool, "bucket_reconciler", uuid.New())

	// First claim → INSERT path (the 7-column VALUES + RETURNING that was
	// truncated). Must acquire at generation 1 with a future expiry.
	gen, exp, ok, err := holderA.claim(ctx)
	if err != nil {
		t.Fatalf("first claim errored (the bug): %v", err)
	}
	if !ok {
		t.Fatal("first claim should acquire an unheld lease")
	}
	if gen != 1 {
		t.Fatalf("first claim generation = %d, want 1", gen)
	}
	if !exp.After(time.Now()) {
		t.Fatalf("expiry %s should be in the future", exp)
	}

	// Same holder re-claims → ON CONFLICT renew path. Generation stays 1
	// (a renewal by the incumbent doesn't bump it).
	gen2, _, ok2, err := holderA.claim(ctx)
	if err != nil || !ok2 {
		t.Fatalf("renew by incumbent failed: ok=%v err=%v", ok2, err)
	}
	if gen2 != 1 {
		t.Fatalf("renew generation = %d, want 1 (unchanged for same holder)", gen2)
	}

	// A different holder, while A's lease is still live, is denied (no row
	// returned → ok=false, no error).
	holderB := newLease(t, pool, "bucket_reconciler", uuid.New())
	_, _, ok3, err := holderB.claim(ctx)
	if err != nil {
		t.Fatalf("contended claim errored: %v", err)
	}
	if ok3 {
		t.Fatal("a second holder must not acquire a live lease")
	}
}
