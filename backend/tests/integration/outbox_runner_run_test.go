//go:build integration

package integration

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// The loop around Tick: it keeps draining while there is work, sleeps only
// when there is none, measures the backlog, and stops when its context does.
//
// One boundary is deliberately not held: the backlog is sampled when 30s have
// passed since the last sample, and whether exactly 30s counts is unreachable
// from a test. The first sample, the one these tests see, happens on the first
// pass whichever way it is written.

const (
	tickFailed        = "outbox tick failed"
	depthSampleFailed = "outbox depth sample failed"
	backlogHigh       = "outbox backlog high"
	// runDeadline bounds every wait on the loop: generous against a loaded
	// runner, and far below what a test hanging on a broken loop would take.
	runDeadline = 10 * time.Second
	runPollStep = 20 * time.Millisecond
)

// runRunner starts r.Run and returns a stop function that cancels it and
// returns what Run returned.
func runRunner(t *testing.T, r *worker.OutboxRunner) func() error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	stopped := false
	stop := func() error {
		if stopped {
			return nil
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(runDeadline):
			t.Fatal("Run did not return after its context was cancelled")
			return nil
		}
	}
	t.Cleanup(func() { _ = stop() })
	return stop
}

// eventually polls cond until it holds or runDeadline passes.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(runDeadline)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(runPollStep)
	}
}

func (f *dispatcherFixture) allDelivered(t *testing.T, tenant uuid.UUID, want int) func() bool {
	t.Helper()
	return func() bool {
		n := 0
		for _, r := range f.allDeliveryRows(t, tenant) {
			if r.Status == "delivered" {
				n++
			}
		}
		return n == want
	}
}

func TestOutboxRunner_RunDrainsUntilCancelled(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	rec := newRecorder(http.StatusOK)
	defer rec.Close()

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "outbox-run")
	f.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL})
	d := f.dispatcher()
	dispatch := func() {
		t.Helper()
		if _, err := d.Dispatch(context.Background(), tenant.String(), makeEvent("", tenant)); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
	}

	core, logs := observer.New(zap.WarnLevel)
	r := f.outboxRunner(d)
	r.Logger = zap.New(core)

	dispatch()
	stop := runRunner(t, r)
	eventually(t, "the first row to be delivered", f.allDelivered(t, tenant, 1))
	// Queued while Run is already going: the loop is still draining.
	dispatch()
	eventually(t, "the second row to be delivered", f.allDelivered(t, tenant, 2))

	if err := stop(); !errors.Is(err, context.Canceled) {
		t.Errorf("Run returned %v, want context.Canceled", err)
	}
	if logs.Len() != 0 {
		t.Errorf("warnings from a healthy run = %v, want none", logs.All())
	}
}

// While a tick finds work the loop goes straight to the next one; it sleeps
// for PollInterval only when a tick found nothing.
func TestOutboxRunner_RunSleepsOnlyWhenIdle(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	rec := newRecorder(http.StatusOK)
	defer rec.Close()

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "outbox-run-busy")
	f.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL})
	d := f.dispatcher()
	const queued = 3
	for range queued {
		if _, err := d.Dispatch(context.Background(), tenant.String(), makeEvent("", tenant)); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
	}

	r := f.outboxRunner(d)
	r.BatchSize = 1
	// Longer than the whole wait below: one sleep between rows fails the test.
	r.PollInterval = time.Hour
	runRunner(t, r)
	eventually(t, "every row to be delivered one tick at a time", f.allDelivered(t, tenant, queued))
}

// claimCounter counts the outbox claim queries a pool runs.
type claimCounter struct{ n atomic.Int64 }

func (c *claimCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "FOR UPDATE SKIP LOCKED") {
		c.n.Add(1)
	}
	return ctx
}

func (c *claimCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// An idle runner with no PollInterval set waits the default between claims
// instead of spinning on an empty table.
func TestOutboxRunner_IdleRunUsesTheDefaultPollInterval(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	cfg, err := pgxpool.ParseConfig(f.h.MigrateDSN)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	claims := &claimCounter{}
	cfg.ConnConfig.Tracer = claims
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	r := f.outboxRunner(f.dispatcher())
	r.Pool = pool
	r.PollInterval = 0
	stop := runRunner(t, r)
	eventually(t, "the first claim", func() bool { return claims.n.Load() >= 1 })
	time.Sleep(worker.DefaultOutboxPollInterval / 2)
	_ = stop()

	// Half a default interval after the first claim, a sleeping loop has not
	// claimed again; a spinning one has claimed hundreds of times. The bound
	// leaves room for one more claim on a runner slow enough to cross the
	// interval while the first one is being detected.
	const sleepingLoopMaxClaims = 2
	if n := claims.n.Load(); n > sleepingLoopMaxClaims {
		t.Errorf("claims in half a poll interval = %d, want at most %d", n, sleepingLoopMaxClaims)
	}
}

// A tenant whose pending fan-out reaches the threshold is reported. The rows
// are not due yet, so the loop's own ticks leave the count where it is.
func TestOutboxRunner_WarnsAtTheBacklogThreshold(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "outbox-backlog")
	sub := f.seedSubscription(t, tenant, subOpts{URL: "http://backlog.invalid"})
	if _, err := f.h.PoolMigrate.Exec(context.Background(),
		`INSERT INTO event_deliveries
		   (tenant_id, subscription_id, event_type, event_at, event_payload, next_attempt_at)
		 SELECT $1, $2, 'paladin.object.uploaded', now(), '{}'::jsonb, now() + interval '1 hour'
		   FROM generate_series(1, $3)`,
		tenant, sub, worker.OutboxDepthWarnThreshold,
	); err != nil {
		t.Fatalf("seed backlog: %v", err)
	}

	core, logs := observer.New(zap.WarnLevel)
	r := f.outboxRunner(f.dispatcher())
	r.Logger = zap.New(core)
	runRunner(t, r)

	eventually(t, "the backlog warning", func() bool { return logs.FilterMessageSnippet(backlogHigh).Len() == 1 })
	entry := logs.FilterMessageSnippet(backlogHigh).All()[0]
	if got := entry.ContextMap()["pending_max_per_tenant"]; got != int64(worker.OutboxDepthWarnThreshold) {
		t.Errorf("pending_max_per_tenant = %v, want %d", got, worker.OutboxDepthWarnThreshold)
	}
}
