//go:build integration

package components

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
	"uuid"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/s3adapter"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// noCopy is the copy of a prober fake that never expects one: no object in
// those tests names another bucket than its collection's.
type noCopy struct{}

var errUnexpectedCopy = errors.New("unexpected copy")

func (noCopy) CopyObject(context.Context, objecth.Location, objecth.Location) error {
	return errUnexpectedCopy
}

func (headFunc) CopyObject(context.Context, objecth.Location, objecth.Location) error {
	return errUnexpectedCopy
}

// lateUpload is an upload presigned before the rebind and PUT after it.
const (
	lateKey  = "late.bin"
	lateSize = int64(9)
)

// storeLoc is l as the fake store addresses it.
func storeLoc(l objecth.Location) worker.CopyLocation {
	return worker.CopyLocation{BackendID: l.BackendID, TenantID: l.TenantID, Bucket: l.Bucket, Collection: l.Collection, Key: l.Key}
}

// storeObjects is objecth.Storage and adapters.HeadProber over the fake store
// the migration worker copies through: what a CompleteObject or a reconciler
// HEAD sees is what the migration put there.
type storeObjects struct {
	objecth.Storage
	s        *fakeObjectStore
	notFound error // what a HEAD of nothing wraps
}

func (o storeObjects) Head(_ context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key, _ string) (string, int64, string, string, error) {
	l := worker.CopyLocation{BackendID: backendID, TenantID: tenantID, Bucket: bucket, Collection: collection, Key: key}
	size, ok := o.s.sizeOf(l)
	if !ok {
		return "", 0, "", "", fmt.Errorf("head %s: %w", storeKey(l), o.notFound)
	}
	return "etag-" + bucket, size, "", "", nil
}

func (o storeObjects) CopyObject(ctx context.Context, src, dst objecth.Location) error {
	return o.s.CopyObject(ctx, storeLoc(src), storeLoc(dst))
}

func (o storeObjects) DeleteObject(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key string) error {
	return o.s.DeleteObject(ctx, worker.CopyLocation{BackendID: backendID, TenantID: tenantID, Bucket: bucket, Collection: collection, Key: key})
}

// presignBeforeRebind registers lateKey as UploadObject does, its URL signed
// for the source bucket, and returns its id.
func (f publicMigrationFixture) presignBeforeRebind(t *testing.T, ctx context.Context) uuid.UUID {
	t.Helper()
	size := lateSize
	obj, err := adapters.NewObjectRepo(sqlc.New(f.pool), f.pool).CreateObject(ctx, objecth.CreateObjectArgs{
		TenantID: f.tenant, Collection: "docs", Key: lateKey, ContentType: "application/octet-stream",
		SizeBytes: &size, PresignExpiresAt: time.Now().Add(time.Hour),
		UploadBackendID: f.backend, UploadBucket: f.private,
	})
	if err != nil {
		t.Fatalf("register the upload: %v", err)
	}
	if got := f.uploadAnchor(t, ctx, obj.ObjectID); got != f.private {
		t.Fatalf("upload anchored to %q, want the bucket its URL names, %s", got, f.private)
	}
	return obj.ObjectID
}

// uploadAnchor is the bucket the object's row is anchored to, "" for none.
func (f publicMigrationFixture) uploadAnchor(t *testing.T, ctx context.Context, id uuid.UUID) string {
	t.Helper()
	var bucket *string
	if err := f.pool.QueryRow(ctx, `SELECT b.name FROM objects o LEFT JOIN buckets b ON b.id = o.upload_bucket_id
		WHERE o.id = $1`, id).Scan(&bucket); err != nil {
		t.Fatalf("read the upload's anchor: %v", err)
	}
	if bucket == nil {
		return ""
	}
	return *bucket
}

func (f publicMigrationFixture) stateOf(t *testing.T, ctx context.Context, id uuid.UUID) string {
	t.Helper()
	var state string
	if err := f.pool.QueryRow(ctx, `SELECT state FROM objects WHERE id = $1`, id).Scan(&state); err != nil {
		t.Fatalf("read object state: %v", err)
	}
	return state
}

// migrationState is the tenant's migration's state.
func (f publicMigrationFixture) migrationState(t *testing.T, ctx context.Context) string {
	t.Helper()
	var state string
	if err := f.pool.QueryRow(ctx, `SELECT state FROM tenant_storage_migrations WHERE tenant_id = $1`, f.tenant).Scan(&state); err != nil {
		t.Fatalf("read migration: %v", err)
	}
	return state
}

// runWorker runs the migration worker, its clock past the retention window,
// until stop says so or the deadline passes, and reports whether stop did.
func (f publicMigrationFixture) runWorker(t *testing.T, ctx context.Context, stop func() bool) bool {
	t.Helper()
	w := &worker.StorageMigrationWorker{
		Repo:   adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool),
		Copier: f.store, Deleter: f.store, Header: f.store,
		Interval: 10 * time.Millisecond,
		Now:      func() time.Time { return time.Now().Add(time.Hour) },
	}
	runCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
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
	for runCtx.Err() == nil {
		if stop() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// migrateWithALateUpload presigns an upload for the source, runs the
// migration to completed — the copy skips the PENDING row, the rebind moves
// the collection — then PUTs the upload where its URL says, the source, and
// checks the cleanup waits for it.
func migrateWithALateUpload(t *testing.T, ctx context.Context) (publicMigrationFixture, uuid.UUID) {
	t.Helper()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	id := f.presignBeforeRebind(t, ctx)
	if err := f.start(t, ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	completed := func() bool { return f.migrationState(t, ctx) == worker.MigStateCompleted }
	if !f.runWorker(t, ctx, completed) {
		t.Fatalf("migration at %s, want completed", f.migrationState(t, ctx))
	}
	if got := f.boundTo(t, ctx, "docs"); got != f.dedicated {
		t.Fatalf("docs bound to %s, want %s", got, f.dedicated)
	}
	f.store.put(f.loc(f.private, "docs", lateKey), lateSize)

	// Past the retention window, the cleanup still waits: the upload's bytes
	// are only in the source.
	f.runWorker(t, ctx, func() bool { return false })
	if state := f.migrationState(t, ctx); state != worker.MigStateCompleted {
		t.Fatalf("migration at %s with an upload still naming the source, want completed", state)
	}
	if !f.store.has(f.loc(f.private, "docs", "obj-1")) {
		t.Fatal("cleanup deleted the source while an upload still names it")
	}
	return f, id
}

// assertSettled checks the upload ended where its collection is read from,
// and the migration then cleaned the source, the upload's copy included.
func (f publicMigrationFixture) assertSettled(t *testing.T, ctx context.Context, id uuid.UUID) {
	t.Helper()
	if state := f.stateOf(t, ctx, id); state != string(statemachine.StateAvailable) {
		t.Fatalf("upload is %s, want AVAILABLE", state)
	}
	if size, ok := f.store.sizeOf(f.loc(f.dedicated, "docs", lateKey)); !ok || size != lateSize {
		t.Fatalf("the upload is not in the collection's bucket (size %d, present %v)", size, ok)
	}
	if got := f.uploadAnchor(t, ctx, id); got != "" {
		t.Errorf("a promoted object still anchored to %s", got)
	}
	cleaned := func() bool { return f.migrationState(t, ctx) == worker.MigStateCleaned }
	if !f.runWorker(t, ctx, cleaned) {
		t.Fatalf("migration at %s once the upload settled, want cleaned", f.migrationState(t, ctx))
	}
	for _, key := range []string{"obj-1", lateKey} {
		if f.store.has(f.loc(f.private, "docs", key)) {
			t.Errorf("source docs/%s survived the cleanup", key)
		}
		if !f.store.has(f.loc(f.dedicated, "docs", key)) {
			t.Errorf("docs/%s is not in the dedicated bucket", key)
		}
	}
}

// An upload presigned before a storage migration's rebind used to be lost:
// its URL names the source, the PUT lands there, and CompleteObject HEADed
// the new binding, found nothing, and never promoted it. The row now records
// the bucket its URL names; the completion finds the bytes there and moves
// them into the collection's bucket, and the cleanup waits for it.
func TestCompleteObjectMovesAnUploadPresignedBeforeTheRebind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, id := migrateWithALateUpload(t, ctx)

	st := storeObjects{s: f.store, notFound: objecth.ErrStoredObjectNotFound}
	h := objecth.NewHandler(adapters.NewObjectRepo(sqlc.New(f.pool), f.pool), st, allowAll{}, nil,
		statemachine.New(f.pool), objecth.PresignConfig{})
	h.SetEventProducer(worker.NewEventRecorder(zap.NewNop()))
	h.SetLogger(zap.NewNop())
	tenantCtx := auth.WithPrincipal(ctx, &auth.Principal{Subject: "u1", TenantID: f.tenant})
	obj, err := h.CompleteObject(tenantCtx, objecth.CompleteObjectInput{
		Collection: "docs", ObjectID: id.String(), ETag: "etag-" + f.private,
	})
	if err != nil {
		t.Fatalf("CompleteObject: %v", err)
	}
	if obj.ETag != "etag-"+f.dedicated {
		t.Errorf("promoted with ETag %q, want the copy's in the collection's bucket", obj.ETag)
	}
	f.assertSettled(t, ctx, id)
}

// The same upload, left by its client without a CompleteObject: the
// reconciler settles it once its URL expires, from the bucket the URL named.
func TestReconcilerMovesAnUploadPresignedBeforeTheRebind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, id := migrateWithALateUpload(t, ctx)
	mustExec(t, ctx, f.pool, `UPDATE objects SET presign_expires_at = now() - interval '48 hours' WHERE id = $1`, id)

	probe := adapters.NewReconcilerProbe(sqlc.New(f.pool), storeObjects{s: f.store, notFound: s3adapter.ErrObjectNotFound})
	r := worker.NewReconcilerV2(statemachine.New(f.pool), probe, worker.ReconcilerV2Config{
		PendingGraceTTL: time.Second,
	}, zap.NewNop())
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	f.assertSettled(t, ctx, id)
}
