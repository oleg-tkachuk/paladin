//go:build integration

package components

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/eventtype"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// A purge during a storage migration deletes the object's row once, and its
// bytes from the bucket the collection is bound to — while the migration
// keeps a copy in its other bucket. Nothing walked that copy again: the copy,
// the verify and the cleanup walk rows. These purge a trashed object at each
// point of the migration and find neither bucket holding it once the purge
// drainer has run, and the migration through to cleaned.

// The trashed object the tests purge, past the hard delete's cooling-off.
const (
	purgedKey  = "trashed.bin"
	purgedSize = int64(5)
	// hardDeleteTTL is the cooling-off the tests' hard deleter waits out;
	// the trashed object was deleted well before it.
	hardDeleteTTL = time.Hour
)

// trashedLongAgo uploads key into collection and moves it to the trash a
// day ago, past the hard delete's cooling-off.
func (f publicMigrationFixture) trashedLongAgo(t *testing.T, ctx context.Context, collection, key string, size int64) {
	t.Helper()
	f.upload(t, ctx, collection, key, size)
	f.trash(t, ctx, collection, key)
	mustExec(t, ctx, f.pool, `UPDATE objects SET terminated_at = now() - interval '1 day' WHERE id = $1`,
		f.objectID(t, ctx, collection, key))
}

// purgeTrash runs the lifecycle hard delete over the tenant's trash: rows
// out, bytes out of the bound bucket, debts for the rest.
func (f publicMigrationFixture) purgeTrash(ctx context.Context) error {
	st := storeObjects{s: f.store}
	return (&worker.LifecycleHardDeleter{
		Q: sqlc.New(f.pool), Pool: f.pool, Storage: st, Events: worker.NewEventRecorder(zap.NewNop()),
		TTL: hardDeleteTTL, BatchSize: 10, Logger: zap.NewNop(),
	}).Sweep(ctx)
}

// drainPurges pays every purge debt the way the purge drainer does.
func (f publicMigrationFixture) drainPurges(t *testing.T, ctx context.Context) {
	t.Helper()
	d := &worker.PurgeDrainer{
		Pool: f.pool, Q: sqlc.New(f.pool), Storage: storeObjects{s: f.store},
		Events: worker.NewEventRecorder(zap.NewNop()), BatchSize: 10, MaxBackoff: time.Second, Logger: zap.NewNop(),
	}
	if err := d.Sweep(ctx); err != nil {
		t.Fatalf("drain purges: %v", err)
	}
	var left int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM pending_purges WHERE tenant_id = $1`, f.tenant).Scan(&left); err != nil {
		t.Fatalf("count purge debts: %v", err)
	}
	if left != 0 {
		t.Fatalf("%d purge debts left after the drain", left)
	}
}

// assertPurged checks no bucket holds the purged object, and the purge was
// announced once.
func (f publicMigrationFixture) assertPurged(t *testing.T, ctx context.Context) {
	t.Helper()
	for _, bucket := range []string{f.private, f.dedicated} {
		if f.store.has(f.loc(bucket, "docs", purgedKey)) {
			t.Errorf("%s still holds the purged object", bucket)
		}
	}
	if n := recordedEventCount(t, ctx, f.pool, f.tenant, eventtype.ObjectPurged); n != 1 {
		t.Errorf("%s recorded %d times, want once", eventtype.ObjectPurged, n)
	}
}

// hookCopier copies through the store and runs hook once, after it copied
// the object at key.
type hookCopier struct {
	*fakeObjectStore
	key  string
	once sync.Once
	hook func()
}

func (c *hookCopier) CopyObject(ctx context.Context, src, dst worker.CopyLocation) error {
	if err := c.fakeObjectStore.CopyObject(ctx, src, dst); err != nil {
		return err
	}
	if src.Key == c.key {
		c.once.Do(c.hook)
	}
	return nil
}

// migrate runs the worker, copying through copier, until the migration is
// cleaned, and fails the test on any other end.
func (f publicMigrationFixture) migrate(t *testing.T, ctx context.Context, copier worker.ObjectCopier) {
	t.Helper()
	if state, reason := f.driveWith(t, ctx, copier, f.store); state != worker.MigStateCleaned {
		t.Fatalf("migration left at %s (%s), want cleaned", state, reason)
	}
}

func TestStorageMigrationPurgeLeavesNoBytesBehind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// The copy holds the row while it copies it: the purge waits, then owes
	// the target's copy too. Before, it deleted the source's and the row,
	// and the target's copy stayed with nothing to find it.
	t.Run("purged while its copy is being made", func(t *testing.T) {
		t.Parallel()
		f := seedPublicMigrationFixture(t, ctx)
		f.addCollection(t, ctx, "docs", f.private, false)
		f.trashedLongAgo(t, ctx, "docs", purgedKey, purgedSize)
		if err := f.start(t, ctx); err != nil {
			t.Fatalf("start: %v", err)
		}
		purged := make(chan error, 1)
		copier := &hookCopier{fakeObjectStore: f.store, key: purgedKey, hook: func() {
			// The copier holds the row: the purge can only finish once the
			// copy's transaction commits, so it runs alongside.
			go func() { purged <- f.purgeTrash(ctx) }()
		}}
		f.migrate(t, ctx, copier)
		if err := <-purged; err != nil {
			t.Fatalf("purge: %v", err)
		}
		f.drainPurges(t, ctx)
		f.assertPurged(t, ctx)
	})

	// Purged before the copy reached it: nothing to copy, and the copy's
	// count no longer comes up short of it ("copy incomplete").
	t.Run("purged before the copy reached it", func(t *testing.T) {
		t.Parallel()
		f := seedPublicMigrationFixture(t, ctx)
		f.addCollection(t, ctx, "docs", f.private, false)
		f.trashedLongAgo(t, ctx, "docs", purgedKey, purgedSize)
		if err := f.start(t, ctx); err != nil {
			t.Fatalf("start: %v", err)
		}
		var purgeErr error
		// docs/obj-1 sorts first: purge the trashed object while obj-1 is
		// copied, before the copy gets to it.
		copier := &hookCopier{fakeObjectStore: f.store, key: "obj-1", hook: func() { purgeErr = f.purgeTrash(ctx) }}
		f.migrate(t, ctx, copier)
		if purgeErr != nil {
			t.Fatalf("purge: %v", purgeErr)
		}
		f.drainPurges(t, ctx)
		f.assertPurged(t, ctx)
	})

	// Purged between the rebind and the cleanup — by the lifecycle hard
	// delete or a permanent delete: the purge removes the target's copy, and
	// owes the source's, which the cleanup no longer lists.
	for name, purge := range map[string]func(t *testing.T, f publicMigrationFixture){
		"hard deleted after the rebind": func(t *testing.T, f publicMigrationFixture) {
			t.Helper()
			if err := f.purgeTrash(ctx); err != nil {
				t.Fatalf("purge: %v", err)
			}
		},
		"permanently deleted after the rebind": func(t *testing.T, f publicMigrationFixture) {
			t.Helper()
			h := permanentDeleteHandler(f.pool, storeObjects{s: f.store})
			obj := objecth.Object{
				ObjectID: f.objectID(t, ctx, "docs", purgedKey), TenantID: f.tenant,
				Collection: "docs", Key: purgedKey, State: statemachine.StateDeleted,
			}
			if err := h.PermanentDelete(ctx, f.tenant, obj, 0, false); err != nil {
				t.Fatalf("PermanentDelete: %v", err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := seedPublicMigrationFixture(t, ctx)
			f.addCollection(t, ctx, "docs", f.private, false)
			f.trashedLongAgo(t, ctx, "docs", purgedKey, purgedSize)
			if err := f.start(t, ctx); err != nil {
				t.Fatalf("start: %v", err)
			}
			f.completeWithinRetention(t, ctx)
			if !f.store.has(f.loc(f.private, "docs", purgedKey)) || !f.store.has(f.loc(f.dedicated, "docs", purgedKey)) {
				t.Fatal("the trashed object is not in both buckets before the purge")
			}
			purge(t, f)
			f.drainPurges(t, ctx)
			f.assertPurged(t, ctx)
			f.migrate(t, ctx, f.store)
			if !f.store.has(f.loc(f.dedicated, "docs", "obj-1")) {
				t.Error("the live object is gone from the dedicated bucket")
			}
		})
	}
}

// completeWithinRetention runs the migration to completed with the clock
// inside its retention window, so it stops there, before the cleanup.
func (f publicMigrationFixture) completeWithinRetention(t *testing.T, ctx context.Context) {
	t.Helper()
	w := &worker.StorageMigrationWorker{
		Repo:   adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool),
		Copier: f.store, Deleter: f.store, Header: f.store, Interval: 10 * time.Millisecond, Now: time.Now,
	}
	runCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	done := make(chan struct{})
	go func() {
		_ = w.Run(runCtx)
		close(done)
	}()
	for runCtx.Err() == nil && f.migrationState(t, ctx) != worker.MigStateCompleted {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if state := f.migrationState(t, ctx); state != worker.MigStateCompleted {
		t.Fatalf("migration at %s, want completed", state)
	}
}

// purgeHeldFor is how long the hold test lets a purge wait on a held row
// before it calls the purge blocked: a purge that is not blocked is done in
// a few milliseconds.
const purgeHeldFor = 300 * time.Millisecond

// The copy holds the object's row in Postgres while it copies: a purge
// waits for it, and then owes the copy it made. Without the hold the purge
// deletes the row mid-copy and the copy's bytes outlive it.
func TestStorageMigrationHoldMakesAPurgeWaitForTheCopy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	f.trashedLongAgo(t, ctx, "docs", purgedKey, purgedSize)
	if err := f.start(t, ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	id := f.objectID(t, ctx, "docs", purgedKey)
	repo := adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool)

	copying, release := make(chan struct{}), make(chan struct{})
	held := make(chan error, 1)
	go func() {
		_, err := repo.HoldObject(ctx, f.tenant, worker.ObjectRef{Collection: "docs", Key: purgedKey}, nil,
			func(ctx context.Context) (*string, error) {
				err := f.store.CopyObject(ctx, f.loc(f.private, "docs", purgedKey), f.loc(f.dedicated, "docs", purgedKey))
				close(copying)
				<-release
				return nil, err
			})
		held <- err
	}()
	<-copying
	purged := make(chan error, 1)
	go func() { purged <- f.purgeTrash(ctx) }()
	select {
	case err := <-purged:
		close(release)
		t.Fatalf("the purge finished (%v) while the copy held the row", err)
	case <-time.After(purgeHeldFor):
	}
	if !objectExists(t, ctx, f.pool, id) {
		t.Fatal("the row was deleted while the copy held it")
	}
	close(release)
	if err := <-held; err != nil {
		t.Fatalf("hold: %v", err)
	}
	if err := <-purged; err != nil {
		t.Fatalf("purge: %v", err)
	}
	var owed int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM pending_purges p JOIN buckets b ON b.id = p.bucket_id
		WHERE p.object_id = $1 AND p.migration_copy AND b.name = $2`, id, f.dedicated).Scan(&owed); err != nil {
		t.Fatalf("read purge debts: %v", err)
	}
	if owed != 1 {
		t.Fatalf("%d debts for the target's copy, want 1", owed)
	}
	f.drainPurges(t, ctx)
	f.assertPurged(t, ctx)
}
