//go:build integration

package components

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"
	"uuid"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/multiparth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
	"github.com/oleg-tkachuk/paladin/backend/internal/presignttl"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/s3adapter"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/uploadpolicy"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// The multipart upload initiated before the rebind and completed after it.
const (
	mpKey      = "late-multipart.bin"
	mpSize     = int64(13)
	mpUploadID = "storage-up-late"
)

// mpStore is multiparth.Storage over the fake store: a completion assembles
// the object where the session was anchored, as a backend does.
type mpStore struct {
	storeObjects
}

func (s mpStore) InitiateMultipart(context.Context, string, string, uuid.UUID, string, string, string, string, string) (string, error) {
	return mpUploadID, nil
}

func (s mpStore) CompleteMultipart(_ context.Context, backendID, bucket string, tenantID uuid.UUID, _, collection, key, _ string, _ []multiparth.PartETag) (string, int64, error) {
	s.s.put(worker.CopyLocation{BackendID: backendID, TenantID: tenantID, Bucket: bucket, Collection: collection, Key: key}, mpSize)
	return "etag-" + bucket, mpSize, nil
}

func (s mpStore) AbortMultipart(context.Context, string, string, uuid.UUID, string, string, string) error {
	return nil
}

func (s mpStore) ListMultipartParts(context.Context, string, string, uuid.UUID, string, string, string, int32, int32) ([]multiparth.Part, int32, error) {
	return nil, 0, nil
}

func (s mpStore) PresignPart(context.Context, string, string, uuid.UUID, string, string, string, multiparth.PartBinding, time.Duration) (string, map[string]string, time.Time, error) {
	return "", nil, time.Time{}, nil
}

func (s mpStore) PublicURL(context.Context, string, string, string, uuid.UUID, string, string) (string, error) {
	return "", nil
}

// mpPart is the one part's checksum, as a client sends it.
func mpPart() multiparth.PartETag {
	sum := sha256.Sum256([]byte(mpKey))
	return multiparth.PartETag{PartNumber: 1, ETag: "part-1", ChecksumValue: base64.StdEncoding.EncodeToString(sum[:])}
}

// initiateBeforeRebind opens a multipart session on docs, anchored to the
// source bucket as InitiateMultipartUpload anchors it, and returns its object
// id and upload id.
func (f publicMigrationFixture) initiateBeforeRebind(t *testing.T, ctx context.Context) (uuid.UUID, string) {
	t.Helper()
	sess, err := adapters.NewMultipartRepo(sqlc.New(f.pool), f.pool).InitiateSession(ctx, multiparth.InitiateArgs{
		TenantID: f.tenant, Collection: "docs", Key: mpKey, ContentType: "application/octet-stream",
		TotalParts: 1, PartSizeBytes: mpSize, SizeHint: mpSize, ChecksumAlgo: checksum.SHA256,
	}, uuid.NewV7(), mpUploadID, f.backend, f.private)
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	if got := f.uploadAnchor(t, ctx, sess.ObjectID); got != f.private {
		t.Fatalf("session's object anchored to %q, want %s", got, f.private)
	}
	return sess.ObjectID, sess.UploadID
}

// migrateAroundAMultipartUpload initiates a multipart upload, then runs the
// migration to completed and past its retention window: the cleanup waits,
// since the session's object is PENDING and anchored to the source.
func migrateAroundAMultipartUpload(t *testing.T, ctx context.Context) (publicMigrationFixture, uuid.UUID, string) {
	t.Helper()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	id, uploadID := f.initiateBeforeRebind(t, ctx)
	if err := f.start(t, ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	completed := func() bool { return f.migrationState(t, ctx) == worker.MigStateCompleted }
	if !f.runWorker(t, ctx, completed) {
		t.Fatalf("migration at %s, want completed", f.migrationState(t, ctx))
	}
	f.runWorker(t, ctx, func() bool { return false })
	if state := f.migrationState(t, ctx); state != worker.MigStateCompleted {
		t.Fatalf("migration at %s with a session anchored to the source, want completed", state)
	}
	return f, id, uploadID
}

// assertMultipartSettled checks the upload is readable where its collection
// is bound, that its source copy survives until the target holds it, and that
// the cleanup then leaves the target's alone.
func (f publicMigrationFixture) assertMultipartSettled(t *testing.T, ctx context.Context, id uuid.UUID) {
	t.Helper()
	if state := f.stateOf(t, ctx, id); state != string(statemachine.StateAvailable) {
		t.Fatalf("upload is %s, want AVAILABLE", state)
	}
	if size, ok := f.store.sizeOf(f.loc(f.dedicated, "docs", mpKey)); !ok || size != mpSize {
		t.Fatalf("the upload is not in the collection's bucket (size %d, present %v)", size, ok)
	}
	cleaned := func() bool { return f.migrationState(t, ctx) == worker.MigStateCleaned }
	if !f.runWorker(t, ctx, cleaned) {
		t.Fatalf("migration at %s once the upload settled, want cleaned", f.migrationState(t, ctx))
	}
	if f.store.has(f.loc(f.private, "docs", mpKey)) {
		t.Error("the upload's source copy survived the cleanup")
	}
	if size, ok := f.store.sizeOf(f.loc(f.dedicated, "docs", mpKey)); !ok || size != mpSize {
		t.Errorf("the cleanup took the upload from the dedicated bucket (size %d, present %v)", size, ok)
	}
}

// A multipart upload completed after a storage migration's rebind used to be
// lost: the session assembled the object in the source, where its parts
// were, the promote cleared its anchor, and the cleanup deleted the only
// copy. The completion now moves the object into the collection's bucket
// before promoting it there.
func TestCompleteMultipartAfterTheRebindMovesTheObject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, id, uploadID := migrateAroundAMultipartUpload(t, ctx)

	st := mpStore{storeObjects{s: f.store, notFound: objecth.ErrStoredObjectNotFound}}
	h := multiparth.NewHandler(adapters.NewMultipartRepo(sqlc.New(f.pool), f.pool), st, allowAll{},
		statemachine.New(f.pool), presignttl.Policy{}, uploadpolicy.Limits{})
	h.SetLogger(zap.NewNop())
	tenantCtx := auth.WithPrincipal(ctx, &auth.Principal{Subject: "u1", TenantID: f.tenant})
	obj, err := h.CompleteMultipartUpload(tenantCtx, multiparth.CompleteArgs{UploadID: uploadID, Parts: []multiparth.PartETag{mpPart()}})
	if err != nil {
		t.Fatalf("CompleteMultipartUpload: %v", err)
	}
	if obj.ETag != "etag-"+f.dedicated {
		t.Errorf("promoted with ETag %q, want the copy's in the collection's bucket", obj.ETag)
	}
	if !f.store.has(f.loc(f.private, "docs", mpKey)) {
		t.Fatal("the completion deleted the assembled copy; it is the cleanup's to delete")
	}
	f.assertMultipartSettled(t, ctx, id)
}

// The same upload, its completion cut short after the store assembled it:
// the row stays PENDING and anchored, the cleanup keeps waiting, and the
// reconciler moves the object once the session's deadline passes.
func TestReconcilerMovesAMultipartUploadAssembledBeforeACrash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, id, _ := migrateAroundAMultipartUpload(t, ctx)
	f.store.put(f.loc(f.private, "docs", mpKey), mpSize)
	mustExec(t, ctx, f.pool, `UPDATE objects SET presign_expires_at = now() - interval '48 hours' WHERE id = $1`, id)

	probe := adapters.NewReconcilerProbe(sqlc.New(f.pool), storeObjects{s: f.store, notFound: s3adapter.ErrObjectNotFound})
	r := worker.NewReconcilerV2(statemachine.New(f.pool), probe, worker.ReconcilerV2Config{PendingGraceTTL: time.Second}, zap.NewNop())
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	f.assertMultipartSettled(t, ctx, id)
}

// The cleanup deletes a source copy only once the target holds the object: an
// object made AVAILABLE after the rebind with its bytes left in the source —
// what a promote without the binding check did — is carried to the target
// rather than deleted with the rest.
func TestCleanupCarriesAnObjectWhoseBytesStayedInTheSource(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, id, _ := migrateAroundAMultipartUpload(t, ctx)
	f.store.put(f.loc(f.private, "docs", mpKey), mpSize)
	// The promote the binding check now refuses: no placement, so nothing
	// checks the object is read from where its bytes are.
	if changed, err := statemachine.New(f.pool).PromoteToAvailable(ctx, id, nil, "etag-"+f.private, mpSize, "", "", statemachine.SourceRPC); err != nil || !changed {
		t.Fatalf("promote: changed=%v err=%v", changed, err)
	}
	if f.store.has(f.loc(f.dedicated, "docs", mpKey)) {
		t.Fatal("the target already holds the object; the test needs it only in the source")
	}
	cleaned := func() bool { return f.migrationState(t, ctx) == worker.MigStateCleaned }
	if !f.runWorker(t, ctx, cleaned) {
		t.Fatalf("migration at %s, want cleaned", f.migrationState(t, ctx))
	}
	if size, ok := f.store.sizeOf(f.loc(f.dedicated, "docs", mpKey)); !ok || size != mpSize {
		t.Fatalf("the cleanup deleted the only copy: the target holds size %d, present %v", size, ok)
	}
	if f.store.has(f.loc(f.private, "docs", mpKey)) {
		t.Error("the carried object's source copy survived the cleanup")
	}
}
