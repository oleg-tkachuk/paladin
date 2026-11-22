//go:build integration

package components

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// The workers that delete bytes in the background ask the bucket gate as a
// write of bytes, as DeleteObject with permanent does: a draining or disabled
// backend keeps its debt, untouched, and the first sweep after the backend
// takes writes again pays it. Each test seeds a refused fixture and a healthy
// one, with a batch of one and the refused work listed first, so a refused
// bucket that still sat at the head of the claim would starve the healthy one.

// gateTestBatch is one row per claim: the refused fixture's work comes first,
// so only a claim that leaves it out reaches the healthy fixture's.
const gateTestBatch = 1

// gateTestTTL is the hard-deleter's cooling-off; the seeded rows are older.
const gateTestTTL = time.Hour

// backendRefusal is a backend state the gate refuses a write of bytes in: the
// storage_backends flag that puts a backend there, and the value it takes.
type backendRefusal struct {
	name    string
	column  string
	refused bool
}

var backendRefusals = []backendRefusal{
	{name: "draining", column: "read_only", refused: true},
	{name: "disabled", column: "enabled", refused: false},
}

// setBackendFlagSQL sets one flag on a backend, by name; the flag's column is
// named by backendRefusal.
const setBackendFlagSQL = `UPDATE storage_backends SET %s = $2 WHERE name = $1`

// apply puts the backend in the refused state, or takes it out.
func (r backendRefusal) apply(t *testing.T, ctx context.Context, pool *pgxpool.Pool, backend string, refused bool) {
	t.Helper()
	mustExec(t, ctx, pool, fmt.Sprintf(setBackendFlagSQL, pgx.Identifier{r.column}.Sanitize()), backend, r.refused == refused)
}

// fixtureBackend names the backend a fixture's collection is on.
func fixtureBackend(t *testing.T, ctx context.Context, pool *pgxpool.Pool, f fixture) string {
	t.Helper()
	var name string
	if err := pool.QueryRow(ctx,
		`SELECT sb.name FROM storage_backends sb JOIN buckets b ON b.backend_id = sb.id
		   JOIN collections c ON c.bucket_id = b.id WHERE c.id = $1`,
		f.collectionID).Scan(&name); err != nil {
		t.Fatalf("read fixture backend: %v", err)
	}
	return name
}

// bucketRecorder records the bucket of every storage delete it is asked for.
type bucketRecorder struct {
	mu      sync.Mutex
	buckets []string
}

func (r *bucketRecorder) DeleteObject(_ context.Context, _, bucket string, _ uuid.UUID, _, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buckets = append(r.buckets, bucket)
	return nil
}

func (r *bucketRecorder) deletesIn(bucket string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, b := range r.buckets {
		if b == bucket {
			n++
		}
	}
	return n
}

func fixtureBucket(t *testing.T, ctx context.Context, pool *pgxpool.Pool, f fixture) string {
	t.Helper()
	var name string
	if err := pool.QueryRow(ctx,
		`SELECT b.name FROM buckets b JOIN collections c ON c.bucket_id = b.id WHERE c.id = $1`,
		f.collectionID).Scan(&name); err != nil {
		t.Fatalf("read fixture bucket: %v", err)
	}
	return name
}

// debtState is what a deferral must leave as it was.
type debtState struct {
	attempts int32
	due      time.Time
}

func readDebt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) debtState {
	t.Helper()
	var d debtState
	if err := pool.QueryRow(ctx,
		`SELECT attempts, next_attempt_at FROM pending_purges WHERE id = $1`, id).
		Scan(&d.attempts, &d.due); err != nil {
		t.Fatalf("read debt %s: %v", id, err)
	}
	return d
}

func TestPurgeDrainerDefersDebtOnABackendTheGateRefuses(t *testing.T) {
	t.Parallel()
	for _, refusal := range backendRefusals {
		t.Run(refusal.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			pool := startPostgres(t)
			refused, healthy := seedFixture(t, ctx, pool), seedFixture(t, ctx, pool)
			refusedDebt := seedPurgeDebt(t, ctx, pool, refused)
			healthyDebt := seedPurgeDebt(t, ctx, pool, healthy)
			// The refused debt is the older one, so it heads the claim order.
			mustExec(t, ctx, pool,
				`UPDATE pending_purges SET next_attempt_at = now() - interval '1 hour' WHERE id = $1`, refusedDebt)
			refusal.apply(t, ctx, pool, fixtureBackend(t, ctx, pool, refused), true)
			before := readDebt(t, ctx, pool, refusedDebt)

			storage := &bucketRecorder{}
			d := &worker.PurgeDrainer{
				Pool: pool, Q: sqlc.New(pool), Storage: storage,
				BatchSize: gateTestBatch, MaxBackoff: time.Hour, Logger: zap.NewNop(),
			}
			if err := d.Sweep(ctx); err != nil {
				t.Fatalf("Sweep = %v, want nil: a deferral is not a failure", err)
			}

			refusedBucket := fixtureBucket(t, ctx, pool, refused)
			if n := storage.deletesIn(refusedBucket); n != 0 {
				t.Fatalf("%d storage deletes on a %s backend, want none", n, refusal.name)
			}
			if got := readDebt(t, ctx, pool, refusedDebt); got != before {
				t.Errorf("deferred debt = %+v, want it untouched at %+v: no attempt counted, no backoff", got, before)
			}
			if got := purgeRowCount(t, ctx, pool, healthyDebt); got != 0 {
				t.Errorf("healthy debt rows = %d, want 0: a refused bucket starved the one behind it", got)
			}

			refusal.apply(t, ctx, pool, fixtureBackend(t, ctx, pool, refused), false)
			if err := d.Sweep(ctx); err != nil {
				t.Fatalf("Sweep after the backend took writes again = %v", err)
			}
			if got := purgeRowCount(t, ctx, pool, refusedDebt); got != 0 {
				t.Errorf("debt rows = %d after the backend took writes again, want 0", got)
			}
			if n := storage.deletesIn(refusedBucket); n != 1 {
				t.Errorf("%d storage deletes once writable, want 1", n)
			}
		})
	}
}

// seedTrashedObject inserts an object trashed long enough ago that the
// hard-deleter takes it, and returns its id.
func seedTrashedObject(t *testing.T, ctx context.Context, pool *pgxpool.Pool, f fixture, age time.Duration) uuid.UUID {
	t.Helper()
	id := uuid.NewV7()
	mustExec(t, ctx, pool,
		`INSERT INTO objects (id, tenant_id, collection_id, path, state, content_type, checksum_algorithm, terminated_at)
		 VALUES ($1, $2, $3, $4, 'DELETED', 'application/octet-stream', 0, now() - $5::interval)`,
		id, f.tenantID, f.collectionID, "trashed-"+uuid.New().String()[:8],
		pgtype.Interval{Microseconds: age.Microseconds(), Valid: true})
	return id
}

func TestLifecycleHardDeleterLeavesTheTrashOnABackendTheGateRefuses(t *testing.T) {
	t.Parallel()
	for _, refusal := range backendRefusals {
		t.Run(refusal.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			pool := startPostgres(t)
			refused, healthy := seedFixture(t, ctx, pool), seedFixture(t, ctx, pool)
			// The refused row was trashed first, so it heads the listing.
			refusedObject := seedTrashedObject(t, ctx, pool, refused, 3*gateTestTTL)
			healthyObject := seedTrashedObject(t, ctx, pool, healthy, 2*gateTestTTL)
			refusal.apply(t, ctx, pool, fixtureBackend(t, ctx, pool, refused), true)

			storage := &bucketRecorder{}
			w := &worker.LifecycleHardDeleter{
				Q: sqlc.New(pool), Pool: pool, Storage: storage,
				TTL: gateTestTTL, BatchSize: gateTestBatch, Logger: zap.NewNop(),
			}
			if err := w.Sweep(ctx); err != nil {
				t.Fatalf("Sweep = %v, want nil: a deferral is not a failure", err)
			}

			refusedBucket := fixtureBucket(t, ctx, pool, refused)
			if n := storage.deletesIn(refusedBucket); n != 0 {
				t.Fatalf("%d storage deletes on a %s backend, want none", n, refusal.name)
			}
			if !objectExists(t, ctx, pool, refusedObject) {
				t.Error("the row went on a refused backend: it must stay in the trash with its bytes")
			}
			var debts int64
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM pending_purges WHERE object_id = $1`, refusedObject).Scan(&debts); err != nil {
				t.Fatalf("count debts: %v", err)
			}
			if debts != 0 {
				t.Errorf("%d purge debts recorded for a deferred row, want none", debts)
			}
			if objectExists(t, ctx, pool, healthyObject) {
				t.Error("the healthy row is still there: a refused bucket starved the one behind it")
			}

			refusal.apply(t, ctx, pool, fixtureBackend(t, ctx, pool, refused), false)
			if err := w.Sweep(ctx); err != nil {
				t.Fatalf("Sweep after the backend took writes again = %v", err)
			}
			if objectExists(t, ctx, pool, refusedObject) {
				t.Error("the row is still there after the backend took writes again")
			}
			if n := storage.deletesIn(refusedBucket); n != 1 {
				t.Errorf("%d storage deletes once writable, want 1", n)
			}
		})
	}
}

// rowTracer records, per statement, the rows each SELECT returned, so a test
// can pin that a sweep reads no more than its batch at a time.
type rowTracer struct {
	mu   sync.Mutex
	sqls []string
	rows []int64
}

type rowTracerKey struct{}

func (r *rowTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, rowTracerKey{}, d.SQL)
}

func (r *rowTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	sql, _ := ctx.Value(rowTracerKey{}).(string)
	if d.Err != nil || !d.CommandTag.Select() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sqls = append(r.sqls, sql)
	r.rows = append(r.rows, d.CommandTag.RowsAffected())
}

// claims answers the row counts of the statements whose text names query.
func (r *rowTracer) claims(query string) []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []int64
	for i, sql := range r.sqls {
		if strings.Contains(sql, "-- name: "+query+" ") {
			out = append(out, r.rows[i])
		}
	}
	return out
}

// tracedPool opens a second pool on pool's database that records every SELECT.
func tracedPool(t *testing.T, pool *pgxpool.Pool) (*pgxpool.Pool, *rowTracer) {
	t.Helper()
	tracer := &rowTracer{}
	cfg := pool.Config().Copy()
	cfg.ConnConfig.Tracer = tracer
	traced, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open traced pool: %v", err)
	}
	t.Cleanup(traced.Close)
	return traced, tracer
}

// refusedBacklog is how many rows the refused fixture holds; far more than a
// batch, so a sweep that read them all, or one claim per row, shows in the
// claim counts.
const refusedBacklog = 6

// The bound: every SELECT a sweep issues returns at most a batch, however
// large the refused backlog, and the refused bucket costs one claim, not one
// per row. A pre-query over the whole backlog, or a claim that kept returning
// the refused rows, would break one or the other.
func TestHardDeleteSweepStaysBoundedByItsBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	refused, healthy := seedFixture(t, ctx, pool), seedFixture(t, ctx, pool)
	for range refusedBacklog {
		seedTrashedObject(t, ctx, pool, refused, 3*gateTestTTL)
	}
	healthyObject := seedTrashedObject(t, ctx, pool, healthy, 2*gateTestTTL)
	backendRefusals[0].apply(t, ctx, pool, fixtureBackend(t, ctx, pool, refused), true)

	traced, tracer := tracedPool(t, pool)
	w := &worker.LifecycleHardDeleter{
		Q: sqlc.New(traced), Pool: traced, Storage: &bucketRecorder{},
		TTL: gateTestTTL, BatchSize: gateTestBatch, Logger: zap.NewNop(),
	}
	if err := w.Sweep(ctx); err != nil {
		t.Fatalf("Sweep = %v", err)
	}
	if objectExists(t, ctx, pool, healthyObject) {
		t.Fatal("the healthy row is still there")
	}
	assertBounded(t, tracer, "ListHardDeletable",
		// The refused row, then the healthy one, then the empty claim that
		// ends the sweep.
		[]int64{gateTestBatch, gateTestBatch, 0})
}

func TestPurgeDrainerTickStaysBoundedByItsBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	refused, healthy := seedFixture(t, ctx, pool), seedFixture(t, ctx, pool)
	for range refusedBacklog {
		id := seedPurgeDebt(t, ctx, pool, refused)
		mustExec(t, ctx, pool,
			`UPDATE pending_purges SET next_attempt_at = now() - interval '1 hour' WHERE id = $1`, id)
	}
	healthyDebt := seedPurgeDebt(t, ctx, pool, healthy)
	backendRefusals[0].apply(t, ctx, pool, fixtureBackend(t, ctx, pool, refused), true)

	traced, tracer := tracedPool(t, pool)
	d := &worker.PurgeDrainer{
		Pool: traced, Q: sqlc.New(traced), Storage: &bucketRecorder{},
		BatchSize: gateTestBatch, MaxBackoff: time.Hour, Logger: zap.NewNop(),
	}
	if err := d.Sweep(ctx); err != nil {
		t.Fatalf("Sweep = %v", err)
	}
	if got := purgeRowCount(t, ctx, pool, healthyDebt); got != 0 {
		t.Fatalf("healthy debt rows = %d, want 0", got)
	}
	// The refused debt, then the healthy one; the batch is then full.
	assertBounded(t, tracer, "ListDuePurges", []int64{gateTestBatch, gateTestBatch})
}

// assertBounded pins the claims a sweep issued, by the rows each returned,
// and that no SELECT of the sweep returned more than a batch.
func assertBounded(t *testing.T, tracer *rowTracer, query string, want []int64) {
	t.Helper()
	if got := tracer.claims(query); !slices.Equal(got, want) {
		t.Errorf("%s claims returned %v rows, want %v", query, got, want)
	}
	tracer.mu.Lock()
	defer tracer.mu.Unlock()
	for i, n := range tracer.rows {
		if n > gateTestBatch {
			t.Errorf("a SELECT returned %d rows, more than the batch of %d: %s", n, gateTestBatch, tracer.sqls[i])
		}
	}
}
