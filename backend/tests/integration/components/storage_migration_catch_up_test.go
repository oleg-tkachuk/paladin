//go:build integration

package components

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// The objects the catch-up tests write behind the copy's cursor. The copy
// walks docs in path order: obj-1, then obj-2, whose copy the tests hook;
// by then the cursor has passed obj-1 and a-new.bin.
const (
	behindKey    = "obj-1"
	hookKey      = "obj-2"
	newKey       = "a-new.bin"
	behindSize   = int64(42) // addCollection's object
	newSize      = int64(17)
	overwriteSeq = "seq-overwrite"
)

// guardedDeleter deletes through the store, and records every delete of a
// source copy the target did not hold with the same content: a delete of an
// object's only copy, or of its newest bytes.
type guardedDeleter struct {
	*fakeObjectStore
	target string
	mu     sync.Mutex
	unsafe []string
}

func (d *guardedDeleter) DeleteObject(ctx context.Context, l worker.CopyLocation) error {
	if l.Bucket != d.target {
		t := l
		t.Bucket = d.target
		src, srcErr := d.ObjectETag(ctx, l)
		dst, dstErr := d.ObjectETag(ctx, t)
		if srcErr == nil && (dstErr != nil || dst != src) {
			d.mu.Lock()
			d.unsafe = append(d.unsafe, storeKey(l))
			d.mu.Unlock()
		}
	}
	return d.fakeObjectStore.DeleteObject(ctx, l)
}

// catchUpFixture is a tenant with docs/obj-1 and docs/obj-2 in its shared
// bucket, its migration started.
func catchUpFixture(t *testing.T, ctx context.Context) publicMigrationFixture {
	t.Helper()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	mustExec(t, ctx, f.pool, `INSERT INTO objects (tenant_id, collection_id, path, state, content_type, size_bytes, etag)
		SELECT $1, c.id, $2, 'AVAILABLE', 'image/png', $3, 'etag-2' FROM collections c WHERE c.tenant_id = $1 AND c.name = 'docs'`,
		f.tenant, hookKey, behindSize)
	f.store.put(f.loc(f.private, "docs", hookKey), behindSize)
	if err := f.start(t, ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	return f
}

// migrateWritingBehindTheCursor runs the migration to cleaned, running write
// once the copy has copied obj-2 — inside that copy, so the cursor has
// passed obj-1 and a-new.bin and stands on obj-2. It fails the test on a
// delete of a source copy the target did not hold.
func (f publicMigrationFixture) migrateWritingBehindTheCursor(t *testing.T, ctx context.Context, write func()) {
	t.Helper()
	deleter := &guardedDeleter{fakeObjectStore: f.store, target: f.dedicated}
	if state, reason := f.runWritingBehindTheCursor(t, ctx, write, f.store, deleter); state != worker.MigStateCleaned {
		t.Fatalf("migration at %s (%s), want cleaned", state, reason)
	}
	if len(deleter.unsafe) != 0 {
		t.Fatalf("the cleanup deleted source copies the target did not hold: %v", deleter.unsafe)
	}
}

// runWritingBehindTheCursor is migrateWritingBehindTheCursor's worker run,
// HEADing through header and deleting through deleter, until the migration
// is cleaned or failed or the deadline passes; it returns the state and the
// recorded reason.
func (f publicMigrationFixture) runWritingBehindTheCursor(t *testing.T, ctx context.Context, write func(),
	header worker.ObjectHeader, deleter worker.ObjectDeleter,
) (string, string) {
	t.Helper()
	w := &worker.StorageMigrationWorker{
		Repo:    adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool),
		Copier:  &hookCopier{fakeObjectStore: f.store, key: hookKey, hook: write},
		Deleter: deleter, Header: header, Identity: f.store,
		Interval: 10 * time.Millisecond, CopyBatch: 1,
		Now: func() time.Time { return time.Now().Add(time.Hour) },
	}
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = w.Run(runCtx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()
	var state, reason string
	for runCtx.Err() == nil {
		var msg *string
		if err := f.pool.QueryRow(ctx, `SELECT state, error FROM tenant_storage_migrations WHERE tenant_id = $1`,
			f.tenant).Scan(&state, &msg); err != nil {
			t.Fatalf("read migration: %v", err)
		}
		if msg != nil {
			reason = *msg
		}
		if state == worker.MigStateCleaned || state == worker.MigStateFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return state, reason
}

// assertLatestInTarget checks the dedicated bucket holds key with the bytes
// the source last had for it.
func (f publicMigrationFixture) assertLatestInTarget(t *testing.T, ctx context.Context, key, latest string) {
	t.Helper()
	got, err := f.store.ObjectETag(ctx, f.loc(f.dedicated, "docs", key))
	if err != nil {
		t.Fatalf("docs/%s is not in the dedicated bucket: %v", key, err)
	}
	if got != latest {
		t.Fatalf("the dedicated bucket holds docs/%s as %s, want the latest write, %s", key, got, latest)
	}
}

// An overwrite behind the copy's cursor reached the source only. Of the same
// size, it passed the verify, and the tenant then read the old bytes from
// the target. The catch-up copies it before the rebind.
func TestStorageMigrationCatchesUpAnOverwriteBehindTheCursor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := catchUpFixture(t, ctx)
	id := f.objectID(t, ctx, "docs", behindKey)
	var latest string
	f.migrateWritingBehindTheCursor(t, ctx, func() {
		// A storage event's overwrite under the same key, the same size.
		f.store.put(f.loc(f.private, "docs", behindKey), behindSize)
		latest, _ = f.store.ObjectETag(ctx, f.loc(f.private, "docs", behindKey))
		if _, err := statemachine.New(f.pool).PromoteToAvailable(ctx, id, f.sourcePlacement(), latest, behindSize, "", overwriteSeq, statemachine.SourceEvent); err != nil {
			t.Errorf("overwrite promote: %v", err)
		}
	})
	f.assertLatestInTarget(t, ctx, behindKey, latest)
}

// A new object behind the cursor was missing from the target, and the verify
// rolled the migration back — every time, for a busy tenant. The catch-up
// copies it before the rebind.
func TestStorageMigrationCatchesUpANewObjectBehindTheCursor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := catchUpFixture(t, ctx)
	var latest string
	f.migrateWritingBehindTheCursor(t, ctx, func() {
		size := newSize
		obj, err := adapters.NewObjectRepo(sqlc.New(f.pool), f.pool).CreateObject(ctx, objecth.CreateObjectArgs{
			TenantID: f.tenant, Collection: "docs", Key: newKey, ContentType: "application/octet-stream",
			SizeBytes: &size, PresignExpiresAt: time.Now().Add(time.Hour),
			UploadBackendID: f.backend, UploadBucket: f.private,
		})
		if err != nil {
			t.Errorf("register: %v", err)
			return
		}
		f.store.put(f.loc(f.private, "docs", newKey), newSize)
		latest, _ = f.store.ObjectETag(ctx, f.loc(f.private, "docs", newKey))
		if _, err := statemachine.New(f.pool).PromoteToAvailable(ctx, obj.ObjectID, f.sourcePlacement(), latest, newSize, "", "", statemachine.SourceRPC); err != nil {
			t.Errorf("promote: %v", err)
		}
	})
	f.assertLatestInTarget(t, ctx, newKey, latest)
	if n := f.attempts(t, ctx); n != 0 {
		t.Errorf("the migration failed %d times on the way; want none", n)
	}
}

// countingCopier copies through the store and, after the nth copy of a key,
// runs the hook registered for that key and count.
type countingCopier struct {
	*fakeObjectStore
	mu     sync.Mutex
	copies map[string]int
	hooks  map[string]func()
}

func (c *countingCopier) CopyObject(ctx context.Context, src, dst worker.CopyLocation) error {
	if err := c.fakeObjectStore.CopyObject(ctx, src, dst); err != nil {
		return err
	}
	c.mu.Lock()
	c.copies[src.Key]++
	hook := c.hooks[fmt.Sprintf("%s#%d", src.Key, c.copies[src.Key])]
	c.mu.Unlock()
	if hook != nil {
		hook()
	}
	return nil
}

// An object the catch-up copied and that was overwritten again before the
// rebind is copied again: the catch-up records the write it took, not that
// it took the object. obj-1 and obj-2 are overwritten once the copy is past
// them; while the catch-up copies obj-2, obj-1 — already caught up — is
// overwritten a second time, at the same size.
func TestStorageMigrationCatchesUpAnObjectWrittenAgain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := catchUpFixture(t, ctx)
	const lastKey = "obj-3"
	mustExec(t, ctx, f.pool, `INSERT INTO objects (tenant_id, collection_id, path, state, content_type, size_bytes)
		SELECT $1, c.id, $2, 'AVAILABLE', 'image/png', $3 FROM collections c WHERE c.tenant_id = $1 AND c.name = 'docs'`,
		f.tenant, lastKey, behindSize)
	f.store.put(f.loc(f.private, "docs", lastKey), behindSize)
	sm := statemachine.New(f.pool)
	seq := 0
	overwrite := func(key string) string {
		seq++
		f.store.put(f.loc(f.private, "docs", key), behindSize)
		etag, _ := f.store.ObjectETag(ctx, f.loc(f.private, "docs", key))
		if _, err := sm.PromoteToAvailable(ctx, f.objectID(t, ctx, "docs", key), f.sourcePlacement(), etag, behindSize, "",
			fmt.Sprintf("seq-%03d", seq), statemachine.SourceEvent); err != nil {
			t.Errorf("overwrite %s: %v", key, err)
		}
		return etag
	}
	var latest string
	copier := &countingCopier{fakeObjectStore: f.store, copies: map[string]int{}, hooks: map[string]func(){
		lastKey + "#1": func() { overwrite(behindKey); overwrite(hookKey) },
		hookKey + "#2": func() { latest = overwrite(behindKey) },
	}}
	w := &worker.StorageMigrationWorker{
		Repo:   adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool),
		Copier: copier, Deleter: f.store, Header: f.store, Identity: f.store,
		Interval: 10 * time.Millisecond, CopyBatch: 1,
		Now: func() time.Time { return time.Now().Add(time.Hour) },
	}
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = w.Run(runCtx)
		close(done)
	}()
	for runCtx.Err() == nil && f.migrationState(t, ctx) != worker.MigStateCleaned {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if state := f.migrationState(t, ctx); state != worker.MigStateCleaned {
		t.Fatalf("migration at %s, want cleaned", state)
	}
	if latest == "" {
		t.Fatal("the second overwrite never ran")
	}
	f.assertLatestInTarget(t, ctx, behindKey, latest)
}

// A catch-up copy recorded for a migration no longer as the worker read it —
// another attempt, or another state — is not taken for the current one's.
func TestCatchUpIsNotRecordedForAMigrationThatMoved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := catchUpFixture(t, ctx)
	at := worker.MigrationRead{TenantID: f.tenant, State: worker.MigStateRebinding}
	mustExec(t, ctx, f.pool, `UPDATE tenant_storage_migrations SET state = $2, copy_started_at = now() - interval '1 hour' WHERE tenant_id = $1`,
		f.tenant, worker.MigStateRebinding)
	id := f.objectID(t, ctx, "docs", behindKey)
	f.store.put(f.loc(f.private, "docs", behindKey), behindSize)
	if _, err := statemachine.New(f.pool).PromoteToAvailable(ctx, id, f.sourcePlacement(), "e", behindSize, "", overwriteSeq, statemachine.SourceEvent); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	repo := adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool)
	objs, err := repo.ListUncaughtUp(ctx, at, 10)
	if err != nil || len(objs) != 1 {
		t.Fatalf("ListUncaughtUp = %v, %v; want the overwritten object", objs, err)
	}
	stale := at
	stale.Attempts++
	if _, err := repo.CatchUpObject(ctx, stale, objs[0], func(context.Context) (worker.CopyIdentity, error) { return worker.CopyIdentity{}, nil }); !errors.Is(err, worker.ErrMigrationMoved) {
		t.Fatalf("CatchUpObject for another attempt = %v, want ErrMigrationMoved", err)
	}
	if objs, err := repo.ListUncaughtUp(ctx, at, 10); err != nil || len(objs) != 1 {
		t.Fatalf("after a stale copy ListUncaughtUp = %v, %v; want the object still behind", objs, err)
	}
	if held, err := repo.CatchUpObject(ctx, at, objs[0], func(context.Context) (worker.CopyIdentity, error) { return worker.CopyIdentity{}, nil }); err != nil || !held {
		t.Fatalf("CatchUpObject = %v, %v; want it recorded", held, err)
	}
	if objs, err := repo.ListUncaughtUp(ctx, at, 10); err != nil || len(objs) != 0 {
		t.Fatalf("after the copy ListUncaughtUp = %v, %v; want none", objs, err)
	}
}

// attempts is how often the tenant's migration has failed.
func (f publicMigrationFixture) attempts(t *testing.T, ctx context.Context) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(ctx, `SELECT attempts FROM tenant_storage_migrations WHERE tenant_id = $1`, f.tenant).Scan(&n); err != nil {
		t.Fatalf("read attempts: %v", err)
	}
	return n
}

// A promote still open when the rebind checks for writes the catch-up has not
// copied used to be invisible to the check and commit after it, its bytes
// only in the source. The rebind now waits for it under the migration's row
// and is refused once it commits: the write is copied first.
func TestRebindIsRefusedByAPromoteHeldAcrossItsCheck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	id := f.presignBeforeRebind(t, ctx)
	f.store.put(f.loc(f.private, "docs", lateKey), lateSize)
	at := f.startedMigration(t, ctx, worker.MigStateRebinding)
	// What the copy left: obj-1 in the target.
	if err := f.store.CopyObject(ctx, f.loc(f.private, "docs", "obj-1"), f.loc(f.dedicated, "docs", "obj-1")); err != nil {
		t.Fatalf("copy: %v", err)
	}
	repo := adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool)

	inTx, release := make(chan struct{}), make(chan struct{})
	promoted := heldPromote(ctx, statemachine.New(f.pool), id, f.sourcePlacement(), lateSize, inTx, release)
	<-inTx
	rebound := make(chan error, 1)
	go func() { rebound <- repo.RebindTenant(ctx, at, f.backend, f.dedicated) }()
	select {
	case err := <-rebound:
		close(release)
		t.Fatalf("the rebind did not wait for the promote in flight: %v", err)
	case <-time.After(fenceWait):
	}
	close(release)
	if err := <-promoted; err != nil {
		t.Fatalf("promote: %v", err)
	}
	err := <-rebound
	var behind *worker.CopyBehindError
	if !errors.As(err, &behind) || behind.Objects != 1 {
		t.Fatalf("rebind = %v, want refused for the one object written", err)
	}
	if got := f.boundTo(t, ctx, "docs"); got != f.private {
		t.Fatalf("a refused rebind moved docs to %s", got)
	}

	// The worker copies it, then rebinds.
	if state, reason := f.drive(t, ctx); state != worker.MigStateCleaned {
		t.Fatalf("migration left at %s (%s), want cleaned", state, reason)
	}
	if size, ok := f.store.sizeOf(f.loc(f.dedicated, "docs", lateKey)); !ok || size != lateSize {
		t.Fatalf("the written object is not in the target (size %d, present %v)", size, ok)
	}
}

// A copy the catch-up made, then replaced in the target behind its back, is
// not the one the verify was promised: it fails on the ETag, which the size
// alone would have passed.
func TestVerifyComparesTheCatchUpsCopy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := catchUpFixture(t, ctx)
	id := f.objectID(t, ctx, "docs", behindKey)
	tamper := &tamperingHeader{fakeObjectStore: f.store, loc: f.loc(f.dedicated, "docs", behindKey)}
	state, reason := f.runWritingBehindTheCursor(t, ctx, func() {
		f.store.put(f.loc(f.private, "docs", behindKey), behindSize)
		etag, _ := f.store.ObjectETag(ctx, f.loc(f.private, "docs", behindKey))
		if _, err := statemachine.New(f.pool).PromoteToAvailable(ctx, id, f.sourcePlacement(), etag, behindSize, "", overwriteSeq, statemachine.SourceEvent); err != nil {
			t.Errorf("overwrite promote: %v", err)
		}
	}, tamper, f.store)
	if state != worker.MigStateFailed {
		t.Fatalf("migration left at %s, want failed (rolled back)", state)
	}
	want := fmt.Sprintf("docs/%s content differs", behindKey)
	if len(reason) < len(want) || reason[len("physical verify: "):][:len(want)] != want {
		t.Errorf("reason = %q, want it to name the content of docs/%s", reason, behindKey)
	}
}

// tamperingHeader is the store's HEAD, but replaces the target's copy of loc
// — same size, new content — the first time the verify reads it.
type tamperingHeader struct {
	*fakeObjectStore
	loc  worker.CopyLocation
	once sync.Once
}

func (h *tamperingHeader) HeadObject(ctx context.Context, l worker.CopyLocation) (int64, error) {
	if storeKey(l) == storeKey(h.loc) {
		h.once.Do(func() {
			if size, ok := h.sizeOf(l); ok {
				h.put(l, size)
			}
		})
	}
	return h.fakeObjectStore.HeadObject(ctx, l)
}
