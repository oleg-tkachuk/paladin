//go:build integration

package components

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/eventingest"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/s3adapter"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// The sequencers of the storage events these tests deliver. A bucket orders
// only its own events: the target's sequencer says nothing about the
// source's, and is lower here, so ordering by it alone would take the
// source's older write for the newer.
const (
	lateEventSequencer   = "0000000000000010"
	targetEventSequencer = "0000000000000005"
)

// runToCompleted runs the worker on the real clock — inside the retention
// window, so a completed migration is not cleaned — copying through copier,
// until the migration is completed.
func (f publicMigrationFixture) runToCompleted(t *testing.T, ctx context.Context, copier worker.ObjectCopier) {
	t.Helper()
	w := &worker.StorageMigrationWorker{
		Repo:   adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool),
		Copier: copier, Deleter: f.store, Header: f.store, Identity: f.store,
		Interval: 10 * time.Millisecond, CopyBatch: 1,
	}
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
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

// ingestHandler is the ingest role's handler over the fixture's database and
// fake store, wired as serve_ingest wires it.
func (f publicMigrationFixture) ingestHandler() *eventingest.PromoteHandler {
	sm := statemachine.New(f.pool)
	probe := adapters.NewReconcilerProbe(sqlc.New(f.pool), storeObjects{s: f.store, notFound: s3adapter.ErrObjectNotFound})
	return &eventingest.PromoteHandler{
		Lookup: sqlc.New(f.pool), Transitioner: sm, Logger: zap.NewNop(),
		Probe: probe, Deleter: sm, Late: sm, Mover: probe,
	}
}

// uploadedEvent is the storage event for an overwrite of docs/key in bucket.
func (f publicMigrationFixture) uploadedEvent(bucket, key, etag, sequencer string, size int64) eventingest.CloudEvent {
	return eventingest.CloudEvent{
		ID: "ev-" + bucket + "-" + key + "-" + sequencer, Source: "test", Type: eventingest.EventTypeUploaded,
		SubjectFields: eventingest.SubjectFields{
			TenantID: f.tenant.String(), Bucket: bucket, Collection: "docs", Key: key,
			Etag: etag, SizeBytes: size, Sequencer: sequencer,
		},
	}
}

// An overwrite written to the source while the copy ran, behind its cursor,
// whose storage event arrives only after the rebind, was skipped as from a
// bucket the collection is not bound to: neither the catch-up nor the row
// ever saw it, the target kept the older bytes and the cleanup deleted the
// newer ones. The event is now promoted in the collection's bucket, its bytes
// copied there first.
func TestStorageMigrationPromotesAnOverwriteWhoseEventArrivesAfterTheRebind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := catchUpFixture(t, ctx)
	var latest string
	f.runToCompleted(t, ctx, &hookCopier{fakeObjectStore: f.store, key: hookKey, hook: func() {
		// Overwritten in the source behind the cursor; its event is late.
		f.store.put(f.loc(f.private, "docs", behindKey), behindSize)
		latest, _ = f.store.ObjectETag(ctx, f.loc(f.private, "docs", behindKey))
	}})
	if latest == "" {
		t.Fatal("the overwrite never ran")
	}

	ev := f.uploadedEvent(f.private, behindKey, latest, lateEventSequencer, behindSize)
	if err := f.ingestHandler().Handle(ctx, ev); err != nil {
		t.Fatalf("handle the late event: %v", err)
	}
	if !f.runWorker(t, ctx, func() bool { return f.migrationState(t, ctx) == worker.MigStateCleaned }) {
		t.Fatalf("migration at %s, want cleaned", f.migrationState(t, ctx))
	}
	f.assertLatestInTarget(t, ctx, behindKey, latest)
}

// A source event can be older than a write the tenant made in the target
// after the rebind: moving it would put the older bytes over the newer. The
// object's row records the target's write, after the rebind, and the late
// event is skipped.
func TestStorageMigrationSkipsALateSourceEventTheTargetSuperseded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := catchUpFixture(t, ctx)
	f.runToCompleted(t, ctx, &hookCopier{fakeObjectStore: f.store, key: hookKey, hook: func() {
		f.store.put(f.loc(f.private, "docs", behindKey), behindSize)
	}})
	stale, _ := f.store.ObjectETag(ctx, f.loc(f.private, "docs", behindKey))

	// Overwritten in the target since the rebind, and promoted there.
	f.store.put(f.loc(f.dedicated, "docs", behindKey), behindSize)
	newer, _ := f.store.ObjectETag(ctx, f.loc(f.dedicated, "docs", behindKey))
	id := f.objectID(t, ctx, "docs", behindKey)
	if changed, err := statemachine.New(f.pool).PromoteToAvailable(ctx, id, f.targetPlacement(), newer, behindSize, "",
		targetEventSequencer, statemachine.SourceEvent); err != nil || !changed {
		t.Fatalf("target overwrite = %v, %v; want promoted", changed, err)
	}

	if err := f.ingestHandler().Handle(ctx, f.uploadedEvent(f.private, behindKey, stale, lateEventSequencer, behindSize)); err != nil {
		t.Fatalf("handle the late event: %v", err)
	}
	f.assertLatestInTarget(t, ctx, behindKey, newer)
	if !f.runWorker(t, ctx, func() bool { return f.migrationState(t, ctx) == worker.MigStateCleaned }) {
		t.Fatalf("migration at %s, want cleaned", f.migrationState(t, ctx))
	}
	f.assertLatestInTarget(t, ctx, behindKey, newer)
}

// rebindingLookup is the handler's lookup, but runs rebind after the first
// read of a collection's binding: the rebind lands between the event's
// bucket check and its promote.
type rebindingLookup struct {
	eventingest.ObjectLookup
	once   sync.Once
	rebind func()
}

func (l *rebindingLookup) GetCollection(ctx context.Context, tenant pgtype.UUID, collection string) (sqlc.GetCollectionRow, error) {
	row, err := l.ObjectLookup.GetCollection(ctx, tenant, collection)
	l.once.Do(l.rebind)
	return row, err
}

// An event checked against the source binding whose promote the rebind then
// refused (ErrBindingMoved) was skipped for the reconciler, which settles
// PENDING uploads only, never an overwrite of an AVAILABLE object. It is now
// moved into the target and promoted there, as any late source write.
func TestStorageMigrationMovesAnOverwriteTheRebindOvertook(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	at := f.startedMigration(t, ctx, worker.MigStateRebinding)
	// What the copy left: obj-1 in the target.
	if err := f.store.CopyObject(ctx, f.loc(f.private, "docs", behindKey), f.loc(f.dedicated, "docs", behindKey)); err != nil {
		t.Fatalf("copy: %v", err)
	}
	f.store.put(f.loc(f.private, "docs", behindKey), behindSize)
	latest, _ := f.store.ObjectETag(ctx, f.loc(f.private, "docs", behindKey))

	h := f.ingestHandler()
	h.Lookup = &rebindingLookup{ObjectLookup: h.Lookup, rebind: func() {
		if err := adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool).RebindTenant(ctx, at, f.backend, f.dedicated); err != nil {
			t.Errorf("rebind: %v", err)
		}
	}}
	if err := h.Handle(ctx, f.uploadedEvent(f.private, behindKey, latest, lateEventSequencer, behindSize)); err != nil {
		t.Fatalf("handle the event: %v", err)
	}
	if got := f.boundTo(t, ctx, "docs"); got != f.dedicated {
		t.Fatalf("docs bound to %s, want %s: the rebind did not run", got, f.dedicated)
	}
	f.assertLatestInTarget(t, ctx, behindKey, latest)
}
