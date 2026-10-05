//go:build integration

package components

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// The two workers that close S3-side multipart sessions nothing else will:
// the reaper, for sessions their client abandoned, and the abort drainer, for
// sessions a database cascade deleted. Both must keep their record on any
// failure, because it is the only record of parts accruing charges.

// abortCall is one AbortMultipart the storage double received.
type abortCall struct {
	backend, bucket, storageUploadID, collection, key string
	tenant                                            uuid.UUID
}

// recordingAborter records aborts, failing the first `failures` of them.
type recordingAborter struct {
	failures int
	mu       sync.Mutex
	calls    []abortCall
}

func (a *recordingAborter) AbortMultipart(_ context.Context, backend, bucket string, tenant uuid.UUID, storageUploadID, collection, key string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, abortCall{backend, bucket, storageUploadID, collection, key, tenant})
	if len(a.calls) <= a.failures {
		return errors.New("backend unreachable")
	}
	return nil
}

// multipartSeed is one multipart session and what it names.
type multipartSeed struct {
	upload    uuid.UUID
	object    uuid.UUID
	storageID string
	tenant    uuid.UUID
	backend   string
	bucket    string
	collect   string
	path      string
}

// seedMultipart inserts a multipart session for a PENDING object, created age
// ago.
func seedMultipart(t *testing.T, ctx context.Context, pool *pgxpool.Pool, age time.Duration) multipartSeed {
	t.Helper()
	s := multipartSeed{
		upload:    uuid.New(),
		object:    uuid.New(),
		storageID: "s3-upload-" + uuid.NewString()[:8],
		backend:   "be-" + uuid.NewString()[:8],
		bucket:    "bk-" + uuid.NewString()[:8],
		collect:   "col-" + uuid.NewString()[:8],
		path:      "k-" + uuid.NewString()[:8],
	}
	s.tenant, _ = mkTenant(t, ctx, pool, "shared")
	mustExec(t, ctx, pool,
		`INSERT INTO storage_backends (name, kind, provider, endpoint, region)
		 VALUES ($1,'s3-compatible','garage','http://x.invalid:3900','us-east-1')`, s.backend)
	mustExec(t, ctx, pool,
		`INSERT INTO buckets (backend_id, name) SELECT id, $2 FROM storage_backends WHERE name = $1`,
		s.backend, s.bucket)
	mustExec(t, ctx, pool,
		`INSERT INTO collections (tenant_id, name, bucket_id) SELECT $1, $2, id FROM buckets WHERE name = $3`,
		s.tenant, s.collect, s.bucket)
	mustExec(t, ctx, pool,
		`INSERT INTO objects (id, tenant_id, collection_id, path, state, content_type)
		 SELECT $1, $2, id, $4, 'PENDING', 'application/octet-stream' FROM collections WHERE tenant_id = $2 AND name = $3`,
		s.object, s.tenant, s.collect, s.path)
	mustExec(t, ctx, pool,
		`INSERT INTO multipart_uploads
		   (id, tenant_id, object_id, bucket_id, storage_upload_id, part_size_bytes,
		    total_parts, initiated_by_subject, initiated_by_kind, collection_name, path, created_at)
		 SELECT $1, $2, $3, b.id, $4, 5242880, 2, 'e2e', 'user', $5, $6, now() - $8::interval
		 FROM buckets b WHERE b.name = $7`,
		s.upload, s.tenant, s.object, s.storageID, s.collect, s.path, s.bucket, age.String())
	return s
}

func countWhere(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string, arg any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, sql, arg).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

const (
	sessionsByID  = `SELECT count(*) FROM multipart_uploads WHERE id = $1`
	debtByStorage = `SELECT count(*) FROM pending_multipart_aborts WHERE storage_upload_id = $1`
	reaperTTL     = 24 * time.Hour
	staleAge      = 48 * time.Hour
	freshAge      = time.Minute
)

func newReaper(pool *pgxpool.Pool, storage worker.MultipartAborter) *worker.MultipartReaper {
	q := sqlc.New(pool)
	return &worker.MultipartReaper{
		Q: q, Sessions: adapters.NewMultipartRepo(q, pool), Storage: storage,
		TTL: reaperTTL, BatchSize: worker.DefaultMultipartReaperBatchSize, Logger: zap.NewNop(),
	}
}

// A stale session is aborted on the backend that holds its parts, then its
// row goes — without filing an abort debt, since the abort already happened.
// A session inside the TTL is left alone.
func TestMultipartReaperAbortsStaleSessions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	stale := seedMultipart(t, ctx, pool, staleAge)
	fresh := seedMultipart(t, ctx, pool, freshAge)

	aborter := &recordingAborter{}
	newReaper(pool, aborter).Sweep(ctx)

	if got := countWhere(t, ctx, pool, sessionsByID, stale.upload); got != 0 {
		t.Errorf("stale session rows = %d, want 0", got)
	}
	if got := countWhere(t, ctx, pool, debtByStorage, stale.storageID); got != 0 {
		t.Errorf("abort debt rows = %d, want 0 — the drainer would abort it again", got)
	}
	if got := countWhere(t, ctx, pool, sessionsByID, fresh.upload); got != 1 {
		t.Errorf("fresh session rows = %d, want 1", got)
	}
	want := abortCall{stale.backend, stale.bucket, stale.storageID, stale.collect, stale.path, stale.tenant}
	if len(aborter.calls) != 1 || aborter.calls[0] != want {
		t.Errorf("aborts = %+v, want only %+v", aborter.calls, want)
	}
}

// An abort that fails keeps the session row, so the next sweep retries it.
func TestMultipartReaperKeepsASessionItCouldNotAbort(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	stale := seedMultipart(t, ctx, pool, staleAge)

	newReaper(pool, &recordingAborter{failures: 1}).Sweep(ctx)

	if got := countWhere(t, ctx, pool, sessionsByID, stale.upload); got != 1 {
		t.Errorf("session rows = %d, want 1 — the parts would be orphaned", got)
	}
}

// seedAbortDebt makes the trigger file a debt: the object goes, and its
// session with it, by cascade.
func seedAbortDebt(t *testing.T, ctx context.Context, pool *pgxpool.Pool) multipartSeed {
	t.Helper()
	s := seedMultipart(t, ctx, pool, freshAge)
	mustExec(t, ctx, pool, `DELETE FROM objects WHERE id = $1`, s.object)
	if got := countWhere(t, ctx, pool, debtByStorage, s.storageID); got != 1 {
		t.Fatalf("seeded debt rows = %d, want 1", got)
	}
	return s
}

func newAbortDrainer(pool *pgxpool.Pool, storage worker.MultipartAborter) *worker.MultipartAbortDrainer {
	return &worker.MultipartAbortDrainer{
		Pool: pool, Q: sqlc.New(pool), Storage: storage,
		BatchSize: worker.DefaultMultipartAbortBatchSize, MaxBackoff: worker.DefaultMultipartAbortMaxBackoff,
		Logger: zap.NewNop(),
	}
}

// A failed abort keeps the debt, counts the attempt, records why, and puts
// the next one in the future.
func TestMultipartAbortDrainerKeepsDebtWhenTheAbortFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	s := seedAbortDebt(t, ctx, pool)

	newAbortDrainer(pool, &recordingAborter{failures: 1}).Sweep(ctx)

	var attempts int32
	var lastErr string
	var due bool
	if err := pool.QueryRow(ctx,
		`SELECT attempts, last_error, next_attempt_at <= now() FROM pending_multipart_aborts WHERE storage_upload_id = $1`,
		s.storageID).Scan(&attempts, &lastErr, &due); err != nil {
		t.Fatalf("read debt: %v — a failed abort must stay owed", err)
	}
	if attempts != 1 || lastErr == "" || due {
		t.Errorf("attempts %d, last_error %q, due again now %v", attempts, lastErr, due)
	}
}

// An abort that lands settles the debt; one that failed is retried once due.
func TestMultipartAbortDrainerSettlesOnceTheAbortLands(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	s := seedAbortDebt(t, ctx, pool)

	aborter := &recordingAborter{failures: 1}
	d := newAbortDrainer(pool, aborter)
	d.Sweep(ctx)
	mustExec(t, ctx, pool, `UPDATE pending_multipart_aborts SET next_attempt_at = now() WHERE storage_upload_id = $1`, s.storageID)
	d.Sweep(ctx)

	if got := countWhere(t, ctx, pool, debtByStorage, s.storageID); got != 0 {
		t.Errorf("debt rows = %d, want 0", got)
	}
	want := abortCall{s.backend, s.bucket, s.storageID, s.collect, s.path, s.tenant}
	if len(aborter.calls) != 2 || aborter.calls[1] != want {
		t.Errorf("aborts = %+v, want two of %+v", aborter.calls, want)
	}
}

// ─── what each sweep tells an operator ─────────────────────────────────────

const (
	drainerAborted   = "aborted an orphaned multipart upload"
	drainerAbortFail = "multipart abort drainer: abort failed; debt kept"
	drainerBackoff   = "multipart abort drainer: backoff"
	drainerSettle    = "multipart abort drainer: settle"
	drainerCommit    = "multipart abort drainer: commit"
	reaperReaped     = "reaped abandoned multipart upload"
	reaperDeleteFail = "delete multipart session row failed"
)

// A sweep warns about what failed and nothing else: a warning on success
// trains an operator to ignore the one that matters.
func TestMultipartAbortDrainerLogsOnlyWhatFailed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	s := seedAbortDebt(t, ctx, pool)

	core, logs := observer.New(zap.InfoLevel)
	d := newAbortDrainer(pool, &recordingAborter{failures: 1})
	d.Logger = zap.New(core)
	d.Sweep(ctx)
	if logs.FilterMessage(drainerAbortFail).Len() != 1 {
		t.Errorf("the failed abort was not reported: %v", logs.All())
	}
	mustExec(t, ctx, pool, `UPDATE pending_multipart_aborts SET next_attempt_at = now() WHERE storage_upload_id = $1`, s.storageID)
	d.Sweep(ctx)
	if logs.FilterMessage(drainerAborted).Len() != 1 {
		t.Errorf("the settled abort was not reported: %v", logs.All())
	}
	for _, msg := range []string{drainerBackoff, drainerSettle, drainerCommit} {
		if n := logs.FilterMessage(msg).Len(); n != 0 {
			t.Errorf("%q logged %d times by sweeps where it did not happen", msg, n)
		}
	}
}

// failingSessions refuses every delete.
type failingSessions struct{}

func (failingSessions) DeleteSession(context.Context, string) error {
	return errors.New("database unavailable")
}

// The abort landed but the row could not go: the reaper says so, and does not
// claim the session reaped.
func TestMultipartReaperReportsARowItCouldNotDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	seedMultipart(t, ctx, pool, staleAge)

	core, logs := observer.New(zap.InfoLevel)
	r := newReaper(pool, &recordingAborter{})
	r.Sessions, r.Logger = failingSessions{}, zap.New(core)
	r.Sweep(ctx)
	if logs.FilterMessage(reaperDeleteFail).Len() != 1 || logs.FilterMessage(reaperReaped).Len() != 0 {
		t.Errorf("log = %v", logs.All())
	}
}

// Without a Logger both workers log to nowhere rather than panicking on the
// first thing they have to say.
func TestMultipartWorkersRunWithoutALogger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	seedMultipart(t, ctx, pool, staleAge)
	seedAbortDebt(t, ctx, pool)

	r := newReaper(pool, &recordingAborter{failures: 1})
	r.Logger = nil
	r.Sweep(ctx)
	d := newAbortDrainer(pool, &recordingAborter{failures: 1})
	d.Logger = nil
	d.Sweep(ctx)
}
