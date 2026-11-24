//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/eventingest"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/s3adapter"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// These drive the storage migration of ADR-0051 a tick at a time — the
// worker, the reconciler and the ingest handler called in turn, never on a
// clock — through the writes a migration used to lose.

// The windows the tests run with: URLs signed before the migration live an
// hour; the reconciler settles an anchor as soon as its URL has expired.
const (
	movesURLWindow = time.Hour
	movesNoGrace   = time.Duration(0)
	// maxMoveTicks bounds the ticks a test drives a migration with before it
	// gives up on the state it waits for.
	maxMoveTicks = 20
	lateKey      = "late.bin"
	lateSize     = int64(9)
)

// contentHead is adapters.HeadProber over the fake store: a HEAD reports the
// content a location holds as its ETag, the one the worker's Identity reads.
type contentHead struct{ s *fakeObjectStore }

func (h contentHead) Head(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key, _ string) (string, int64, string, string, error) {
	l := worker.CopyLocation{BackendID: backendID, TenantID: tenantID, Bucket: bucket, Collection: collection, Key: key}
	size, ok := h.s.sizeOf(l)
	if !ok {
		return "", 0, "", "", s3adapter.ErrObjectNotFound
	}
	etag, err := h.s.ObjectETag(ctx, l)
	return etag, size, "", "", err
}

func (h contentHead) DeleteObject(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key string) error {
	return h.s.DeleteObject(ctx, worker.CopyLocation{BackendID: backendID, TenantID: tenantID, Bucket: bucket, Collection: collection, Key: key})
}

// moveWorker is the migration worker over the fake store.
func (f publicMigrationFixture) moveWorker(now func() time.Time) *worker.StorageMigrationWorker {
	return &worker.StorageMigrationWorker{
		Repo:   adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool),
		Copier: f.store, Deleter: f.store, Header: f.store, Identity: f.store,
		WriteWindow: movesURLWindow, SettleGrace: movesNoGrace, Now: now, Logger: zap.NewNop(),
	}
}

// tick runs one tick of w.
func (f publicMigrationFixture) tick(t *testing.T, ctx context.Context, w *worker.StorageMigrationWorker) {
	t.Helper()
	if err := w.Tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
}

// tickUntil ticks w until the migration is in state, failing past
// maxMoveTicks.
func (f publicMigrationFixture) tickUntil(t *testing.T, ctx context.Context, w *worker.StorageMigrationWorker, state string) {
	t.Helper()
	for range maxMoveTicks {
		if f.migrationState(t, ctx) == state {
			return
		}
		f.tick(t, ctx, w)
	}
	m := f.active(t, ctx)
	from, _, _ := m.Source, m.Target, m.State
	if m.State == worker.MigStateRollingBack {
		from = m.Target
	}
	holds, err := adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool).Holds(ctx, m.Read(), from, movesNoGrace)
	t.Fatalf("migration at %s after %d ticks, want %s; what holds %s: %+v (%v)", m.State, maxMoveTicks, state, from.Bucket, holds, err)
}

// enterMoving starts the migration and ticks it into 'moving'; it returns
// the migration as the worker reads it.
func (f publicMigrationFixture) enterMoving(t *testing.T, ctx context.Context) worker.StorageMigration {
	t.Helper()
	if err := f.start(t, ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	f.tickUntil(t, ctx, f.moveWorker(time.Now), worker.MigStateMoving)
	return f.active(t, ctx)
}

// active reads the tenant's migration as the worker lists it.
func (f publicMigrationFixture) active(t *testing.T, ctx context.Context) worker.StorageMigration {
	t.Helper()
	migs, err := adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool).ListActive(ctx, maxMoveTicks)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	for _, m := range migs {
		if m.TenantID == f.tenant {
			return m
		}
	}
	t.Fatal("the tenant's migration is not active")
	return worker.StorageMigration{}
}

// registerUpload registers an upload of key in docs whose URL is signed for
// bucket and valid for movesURLWindow, as UploadObject does.
func (f publicMigrationFixture) registerUpload(t *testing.T, ctx context.Context, key, bucket string) uuid.UUID {
	t.Helper()
	size := lateSize
	obj, err := adapters.NewObjectRepo(sqlc.New(f.pool), f.pool).CreateObject(ctx, objecth.CreateObjectArgs{
		TenantID: f.tenant, Collection: "docs", Key: key, ContentType: "text/plain", SizeBytes: &size,
		PresignExpiresAt: time.Now().Add(movesURLWindow), UploadBackendID: f.backend, UploadBucket: bucket,
	})
	if err != nil {
		t.Fatalf("register %s: %v", key, err)
	}
	return obj.ObjectID
}

// putThroughURL writes key's bytes into bucket, as a client's PUT through a
// URL signed for it does, and returns the content's ETag.
func (f publicMigrationFixture) putThroughURL(t *testing.T, ctx context.Context, key, bucket string) string {
	t.Helper()
	l := f.loc(bucket, "docs", key)
	f.store.put(l, lateSize)
	etag, err := f.store.ObjectETag(ctx, l)
	if err != nil {
		t.Fatalf("read the put's ETag: %v", err)
	}
	return etag
}

// complete promotes id with the bytes in bucket, as CompleteObject does.
func (f publicMigrationFixture) complete(t *testing.T, ctx context.Context, id uuid.UUID, bucket, etag string) {
	t.Helper()
	changed, err := statemachine.New(f.pool).PromoteToAvailable(ctx, id,
		&statemachine.Placement{BackendID: f.backend, Bucket: bucket}, etag, lateSize, "", "", statemachine.SourceRPC)
	if err != nil || !changed {
		t.Fatalf("complete: changed %v, %v", changed, err)
	}
}

// expireURLs ends every URL of the tenant and the migration's write window,
// as the passing of max_ttl does.
func (f publicMigrationFixture) expireURLs(t *testing.T, ctx context.Context) {
	t.Helper()
	mustExec(t, ctx, f.pool, `UPDATE objects SET presign_expires_at = now() - interval '1 minute'
		WHERE tenant_id = $1 AND presign_expires_at IS NOT NULL`, f.tenant)
	mustExec(t, ctx, f.pool, `UPDATE tenant_storage_migrations SET writes_close_at = now() - interval '1 minute'
		WHERE tenant_id = $1`, f.tenant)
}

// settle runs one reconciler tick over the fake store.
func (f publicMigrationFixture) settle(t *testing.T, ctx context.Context) {
	t.Helper()
	r := worker.NewReconcilerV2(statemachine.New(f.pool), adapters.NewReconcilerProbe(sqlc.New(f.pool), contentHead{s: f.store}),
		worker.ReconcilerV2Config{PendingGraceTTL: time.Second}, zap.NewNop())
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

// objectRow is what the tests read of an object's row.
type objectRow struct {
	state, etag, placed string
	anchored            bool
}

func (f publicMigrationFixture) row(t *testing.T, ctx context.Context, id uuid.UUID) objectRow {
	t.Helper()
	var r objectRow
	var etag, placed *string
	if err := f.pool.QueryRow(ctx, `SELECT o.state, o.etag, b.name, o.upload_bucket_id IS NOT NULL
		FROM objects o LEFT JOIN buckets b ON b.id = o.placement_bucket_id WHERE o.id = $1`, id).
		Scan(&r.state, &etag, &placed, &r.anchored); err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	if etag != nil {
		r.etag = *etag
	}
	if placed != nil {
		r.placed = *placed
	}
	return r
}

// A write through a URL signed for the source before the migration started
// lands in the source after the switch: its completion places it there, it
// is not moved while its URL can still write, and once the URL has expired
// and the reconciler settled it, a later pass moves it. The migration waits
// for it — and for the write window — before it completes, and the cleanup
// deletes the source copies only after that.
func TestStorageMigrationMovesALateWriteThroughAnOldURL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	late := f.registerUpload(t, ctx, lateKey, f.private)

	f.enterMoving(t, ctx)
	w := f.moveWorker(time.Now)
	f.tick(t, ctx, w)
	if got := f.boundTo(t, ctx, "docs"); got != f.dedicated {
		t.Fatalf("docs bound to %s while moving, want the target", got)
	}
	if !f.store.has(f.loc(f.dedicated, "docs", "obj-1")) {
		t.Fatal("the settled object was not moved")
	}

	// The client PUTs through the URL it was handed before the switch.
	etag := f.putThroughURL(t, ctx, lateKey, f.private)
	f.complete(t, ctx, late, f.private, etag)
	for range 3 {
		f.tick(t, ctx, w)
	}
	if r := f.row(t, ctx, late); r.placed != f.private || !r.anchored {
		t.Fatalf("late write %+v, want placed in the source and still anchored", r)
	}
	if state := f.migrationState(t, ctx); state != worker.MigStateMoving {
		t.Fatalf("migration %s while a URL can write the source, want moving", state)
	}

	f.expireURLs(t, ctx)
	f.settle(t, ctx)
	if r := f.row(t, ctx, late); r.anchored || r.etag != etag {
		t.Fatalf("late write %+v after the settle, want settled with ETag %s", r, etag)
	}
	f.tickUntil(t, ctx, w, worker.MigStateCompleted)
	if r := f.row(t, ctx, late); r.placed != f.dedicated {
		t.Fatalf("late write placed in %s, want the target", r.placed)
	}
	if !f.store.has(f.loc(f.dedicated, "docs", lateKey)) {
		t.Fatal("the target lacks the late write")
	}
	if !f.store.has(f.loc(f.private, "docs", lateKey)) {
		t.Fatal("the source copy is gone before cleanup_after")
	}

	later := f.moveWorker(func() time.Time { return time.Now().Add(2 * movesURLWindow) })
	f.tickUntil(t, ctx, later, worker.MigStateCleaned)
	for _, key := range []string{"obj-1", lateKey} {
		if f.store.has(f.loc(f.private, "docs", key)) {
			t.Errorf("the cleanup left %s in the source", key)
		}
		if !f.store.has(f.loc(f.dedicated, "docs", key)) {
			t.Errorf("%s is missing from the target after the cleanup", key)
		}
	}
}

// Two URLs write one key: one of a row deleted and registered again since
// the migration started, signed for the source, and the new row's, signed for
// the target. Each row takes only writes through its own URL, and the last
// promoted wins: the stale URL's bytes in the source are no row's, never
// promoted, and the cleanup removes them.
func TestStorageMigrationTwoURLsForOneKeyLastPromotedWins(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	old := f.registerUpload(t, ctx, lateKey, f.private)
	f.enterMoving(t, ctx)
	// The old row goes before its URL expires; the key is registered again,
	// its new URL signed for the bucket the collection is bound to now.
	mustExec(t, ctx, f.pool, `DELETE FROM objects WHERE id = $1`, old)
	renewed := f.registerUpload(t, ctx, lateKey, f.dedicated)

	h := &eventingest.PromoteHandler{Lookup: sqlc.New(f.pool), Transitioner: statemachine.New(f.pool), Logger: zap.NewNop()}
	event := func(bucket, etag, sequencer string) eventingest.CloudEvent {
		return eventingest.CloudEvent{ID: uuid.NewV7().String(), Type: eventingest.EventTypeUploaded,
			SubjectFields: eventingest.SubjectFields{TenantID: f.tenant.String(), Bucket: bucket, Collection: "docs",
				Key: lateKey, Etag: etag, SizeBytes: lateSize, Sequencer: sequencer}}
	}
	newETag := f.putThroughURL(t, ctx, lateKey, f.dedicated)
	if err := h.Handle(ctx, event(f.dedicated, newETag, "seq-1")); err != nil {
		t.Fatalf("event from the target: %v", err)
	}
	// The stale URL writes the source later, and its event arrives last.
	staleETag := f.putThroughURL(t, ctx, lateKey, f.private)
	if err := h.Handle(ctx, event(f.private, staleETag, "seq-2")); err != nil {
		t.Fatalf("event from the source: %v", err)
	}
	if r := f.row(t, ctx, renewed); r.state != string(statemachine.StateAvailable) || r.placed != f.dedicated || r.etag != newETag {
		t.Fatalf("renewed row %+v, want promoted in the target with ETag %s", r, newETag)
	}

	f.expireURLs(t, ctx)
	f.settle(t, ctx)
	w := f.moveWorker(time.Now)
	f.tickUntil(t, ctx, w, worker.MigStateCompleted)
	f.tickUntil(t, ctx, f.moveWorker(func() time.Time { return time.Now().Add(2 * movesURLWindow) }), worker.MigStateCleaned)
	if r := f.row(t, ctx, renewed); r.etag != newETag || r.placed != f.dedicated {
		t.Fatalf("renewed row %+v after the migration, want its own write in the target", r)
	}
	if f.store.has(f.loc(f.private, "docs", lateKey)) {
		t.Error("the stale URL's bytes stayed in the source")
	}
}

// A write through a URL after its object's promote, whose storage event was
// lost, is found once the URL has expired: the reconciler's settle records
// what the bucket holds — the last write wins — and the migration then moves
// those bytes, not the ones the row recorded first.
func TestStorageMigrationALostEventIsSettledByTheReconciler(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	id := f.registerUpload(t, ctx, lateKey, f.private)
	first := f.putThroughURL(t, ctx, lateKey, f.private)
	f.complete(t, ctx, id, f.private, first)
	// The same URL writes again; the event for it never comes.
	second := f.putThroughURL(t, ctx, lateKey, f.private)

	f.enterMoving(t, ctx)
	f.expireURLs(t, ctx)
	f.settle(t, ctx)
	if r := f.row(t, ctx, id); r.etag != second || r.anchored {
		t.Fatalf("row %+v after the settle, want the second write %s, settled", r, second)
	}
	f.tickUntil(t, ctx, f.moveWorker(time.Now), worker.MigStateCompleted)
	got, err := f.store.ObjectETag(ctx, f.loc(f.dedicated, "docs", lateKey))
	if err != nil || got != second {
		t.Fatalf("target holds %q (%v), want the second write %s", got, err, second)
	}
}

// A target that stops taking objects rolls the migration back: uploads go to
// the source again, the objects already moved move back, and the migration
// fails, shared, ready for a retry.
func TestStorageMigrationRollsBackByMovingObjectsBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	f.upload(t, ctx, "docs", "second", lateSize)
	f.enterMoving(t, ctx)
	w := f.moveWorker(time.Now)
	f.tick(t, ctx, w)
	obj := f.objectID(t, ctx, "docs", "obj-1")
	if r := f.row(t, ctx, obj); r.placed != f.dedicated {
		t.Fatalf("obj-1 placed in %s before the rollback, want the target", r.placed)
	}

	mustExec(t, ctx, f.pool, `UPDATE buckets SET provision_state = 'deleting' WHERE name = $1`, f.dedicated)
	f.tick(t, ctx, w)
	if state := f.migrationState(t, ctx); state != worker.MigStateRollingBack {
		t.Fatalf("migration %s with its target deleting, want rolling_back", state)
	}
	mustExec(t, ctx, f.pool, `UPDATE buckets SET provision_state = 'ready' WHERE name = $1`, f.dedicated)
	f.tick(t, ctx, w)
	f.expireURLs(t, ctx)
	f.tickUntil(t, ctx, w, worker.MigStateFailed)
	if r := f.row(t, ctx, obj); r.placed != f.private {
		t.Fatalf("obj-1 placed in %s after the rollback, want the source", r.placed)
	}
	f.assertOnSource(t, ctx, "docs")
}

// A move places the object only if its bytes are still the ones the copy
// read: a write recorded while the copy ran (here a promote's content
// version, committed on another connection, which the move's FOR KEY SHARE
// hold lets through) leaves it where it is, for a later pass.
func TestStorageMigrationMoveIsACompareAndSet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	m := f.enterMoving(t, ctx)
	repo := adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool)
	id := f.objectID(t, ctx, "docs", "obj-1")
	moved, err := repo.MoveObject(ctx, m.Read(), m.Source, m.Target, worker.MoveCandidate{ID: id},
		func(ctx context.Context, _ worker.MoveCandidate) (string, error) {
			_, err := f.pool.Exec(ctx, `UPDATE objects SET bytes_written_at = clock_timestamp() WHERE id = $1`, id)
			return "", err
		})
	if err != nil || moved {
		t.Fatalf("move over a write during its copy: moved %v, %v; want not moved", moved, err)
	}
	if r := f.row(t, ctx, id); r.placed != f.private {
		t.Fatalf("placed in %s, want the source", r.placed)
	}
}

// Every write of a move names the migration as read: a worker that read an
// earlier attempt, or another state, moves nothing.
func TestStorageMigrationMoveAppliesOnlyToTheMigrationRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	m := f.enterMoving(t, ctx)
	repo := adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool)
	id := f.objectID(t, ctx, "docs", "obj-1")
	stale := m.Read()
	stale.Attempts++
	copied := false
	_, err := repo.MoveObject(ctx, stale, m.Source, m.Target, worker.MoveCandidate{ID: id},
		func(context.Context, worker.MoveCandidate) (string, error) { copied = true; return "", nil })
	if !errors.Is(err, worker.ErrMigrationMoved) || copied {
		t.Fatalf("stale move: err %v, copied %v; want ErrMigrationMoved and no copy", err, copied)
	}
	if r := f.row(t, ctx, id); r.placed != f.private {
		t.Fatalf("placed in %s after a stale move, want the source", r.placed)
	}
}
