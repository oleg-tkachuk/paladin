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
	"errors"
	"sync"
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

	"github.com/oleg-tkachuk/paladin/backend/migrations"
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

// Run is the loop the claim SQL exists to serve, and nothing exercised it.
// The unit tests drive the renewer through a fake; the test above drives the
// SQL directly. Between them sits the orchestration that decides whether two
// pods ever run the same job at the same time — which is the entire point of
// a lease, and the one failure that corrupts data rather than merely stopping
// it.

func leaseWithTiming(t *testing.T, pool *pgxpool.Pool, name string, holder uuid.UUID, ttl, poll time.Duration) *Lease {
	t.Helper()
	l, err := New(pool, Config{
		Name: name, HolderID: holder, HolderMeta: map[string]string{"host": "test"},
		TTL: ttl, PollInterval: poll, Logger: zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("New lease: %v", err)
	}
	return l
}

func TestRun_OnlyOneHolderRunsTheWork(t *testing.T) {
	pool := startPostgres(t)
	name := "run-exclusion-" + uuid.NewString()[:8]

	var mu sync.Mutex
	inside, maxInside := 0, 0
	release := make(chan struct{})

	work := func(ctx context.Context, _ int64) error {
		mu.Lock()
		inside++
		if inside > maxInside {
			maxInside = inside
		}
		mu.Unlock()
		<-release // hold the lease until the test says otherwise
		mu.Lock()
		inside--
		mu.Unlock()
		return context.Canceled
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var wg sync.WaitGroup
	for range 3 {
		l := leaseWithTiming(t, pool, name, uuid.New(), time.Minute, 50*time.Millisecond)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = l.Run(ctx, work)
		}()
	}

	// Give the losers time to poll and be refused several times over.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := inside
		mu.Unlock()
		if n > 1 {
			break // fail fast rather than waiting out the window
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	peak := maxInside
	mu.Unlock()
	if peak != 1 {
		t.Fatalf("concurrent workers inside the lease = %d, want exactly 1", peak)
	}

	close(release)
	cancel()
	wg.Wait()
}

func TestRun_ReturnsARealErrorAndSwallowsCancellation(t *testing.T) {
	// The distinction the loop turns on: a genuine failure from the work must
	// reach the caller and stop the worker, while a cancellation means "the
	// lease lapsed or we are shutting down" and must send it back around to
	// re-claim. Confusing the two either crashes a healthy worker on every
	// lease handover, or hides a real fault forever behind a retry loop.
	pool := startPostgres(t)
	boom := errors.New("the work itself failed")

	l := leaseWithTiming(t, pool, "run-err-"+uuid.NewString()[:8], uuid.New(),
		time.Minute, 20*time.Millisecond)
	err := l.Run(context.Background(), func(context.Context, int64) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("Run err = %v, want the work's own error", err)
	}

	// Cancellation instead: Run must loop rather than return, so the only way
	// out is the parent context.
	l2 := leaseWithTiming(t, pool, "run-cancel-"+uuid.NewString()[:8], uuid.New(),
		time.Minute, 20*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	runs := 0
	done := make(chan error, 1)
	go func() {
		done <- l2.Run(ctx, func(context.Context, int64) error {
			runs++
			if runs >= 3 {
				cancel() // let the loop notice the parent is going away
			}
			return context.Canceled
		})
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v, want context.Canceled once the parent stopped", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run never returned after its parent was cancelled")
	}
	if runs < 3 {
		t.Errorf("work ran %d times; a cancelled run must send the loop back to re-claim", runs)
	}
}

func TestRun_ReleasesSoTheNextHolderDoesNotWaitOutTheTTL(t *testing.T) {
	// The lease TTL is a minute; a pod that finished its work must not make
	// the next one wait that long. Run releases on the way out, which is what
	// turns a rolling restart into a handover instead of a stall.
	pool := startPostgres(t)
	name := "run-release-" + uuid.NewString()[:8]

	first := leaseWithTiming(t, pool, name, uuid.New(), time.Minute, 20*time.Millisecond)
	if err := first.Run(context.Background(), func(context.Context, int64) error {
		return errors.New("stop after one turn")
	}); err == nil {
		t.Fatal("expected the work's error")
	}

	// A fresh holder must acquire immediately, not in a minute.
	second := leaseWithTiming(t, pool, name, uuid.New(), time.Minute, 20*time.Millisecond)
	_, _, ok, err := second.claim(context.Background())
	if err != nil {
		t.Fatalf("claim after release: %v", err)
	}
	if !ok {
		t.Fatal("the lease was not released; the next holder must wait out the whole TTL")
	}
}
