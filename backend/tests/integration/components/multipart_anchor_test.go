//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/multiparth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// TestMultipartSessionAnchorsBackend proves the schema baseline (001_initial_schema.sql) + the session
// anchoring: InitiateSession persists the (backend_id, bucket_name) it was
// given, and GetSession reads back exactly those — NOT the collection's
// current binding. This is what keeps complete/abort/presign-part (and the
// reaper) targeting where the parts actually live after an operator rebinds
// the collection to another backend mid-upload.
func TestMultipartSessionAnchorsBackend(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	repo := adapters.NewMultipartRepo(sqlc.New(pool), pool)

	// Anchor values deliberately distinct from the fixture's collection
	// binding, so a match on read can only come from the stored session, not
	// a re-resolution of the current binding.
	//
	// They must nonetheless be a REAL bucket: multipart_uploads.bucket_id is a
	// foreign key now, so anchoring to a bucket that does not exist is no
	// longer expressible — which is the point of the constraint.
	const anchorBackend, anchorBucket = "be-anchored", "bkt-anchored"
	mustExec(t, ctx, pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, anchorBackend)
	mustExec(t, ctx, pool, `INSERT INTO buckets (backend_id, name, owner_tenant_id)
		 SELECT sb.id, $2, $3 FROM storage_backends sb WHERE sb.name = $1`, anchorBackend, anchorBucket, f.tenantID)
	objectID := uuid.NewV7()
	sess, err := repo.InitiateSession(ctx, multiparth.InitiateArgs{
		TenantID:      f.tenantID,
		Collection:    f.collection,
		Key:           "mpu-" + uuid.New().String()[:8],
		ContentType:   "application/octet-stream",
		TotalParts:    2,
		PartSizeBytes: 5 << 20,
		SizeHint:      6 << 20,
	}, objectID, "storage-upload-xyz", anchorBackend, anchorBucket)
	if err != nil {
		t.Fatalf("InitiateSession: %v", err)
	}
	if sess.BackendID != anchorBackend || sess.Bucket != anchorBucket {
		t.Fatalf("InitiateSession returned backend/bucket = %q/%q, want %q/%q",
			sess.BackendID, sess.Bucket, anchorBackend, anchorBucket)
	}

	got, err := repo.GetSession(ctx, sess.UploadID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.BackendID != anchorBackend {
		t.Fatalf("GetSession backend = %q, want %q (anchored at initiate, not the current binding)", got.BackendID, anchorBackend)
	}
	if got.Bucket != anchorBucket {
		t.Fatalf("GetSession bucket = %q, want %q (anchored at initiate)", got.Bucket, anchorBucket)
	}
}

// anchoredMaxParts is the max_parts the anchored bucket's constraints carry,
// so a session read back with it can only have read the anchored bucket's.
const anchoredMaxParts = 7

// TestMultipartSessionCarriesTheAnchoredBucketsGateState rebinds the
// collection mid-session, as a storage migration does, and drains the backend
// the session was anchored to. GetSession answers that bucket's own gate state
// and constraints, so a completion or a part URL is refused there as a write
// of bytes, while an abort, a read, still goes through; the collection's new,
// healthy binding decides nothing about the session.
func TestMultipartSessionCarriesTheAnchoredBucketsGateState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	repo := adapters.NewMultipartRepo(sqlc.New(pool), pool)

	anchorBackend, anchorBucket := fixtureBackend(t, ctx, pool, f), fixtureBucket(t, ctx, pool, f)
	mustExec(t, ctx, pool, `UPDATE buckets SET constraints = jsonb_build_object('MaxParts', $2::int)
		WHERE name = $1`, anchorBucket, anchoredMaxParts)
	sess, err := repo.InitiateSession(ctx, multiparth.InitiateArgs{
		TenantID: f.tenantID, Collection: f.collection, Key: "mpu-" + uuid.New().String()[:8],
		ContentType: "application/octet-stream", TotalParts: 2, PartSizeBytes: 5 << 20, SizeHint: 6 << 20,
	}, uuid.NewV7(), "storage-upload-rebound", anchorBackend, anchorBucket)
	if err != nil {
		t.Fatalf("InitiateSession: %v", err)
	}

	// The migration: the collection moves to a new, healthy backend, and the
	// one the parts are on drains.
	newBackend, newBucket := "be-rebound-"+uuid.New().String()[:8], "bkt-rebound-"+uuid.New().String()[:8]
	mustExec(t, ctx, pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, newBackend)
	mustExec(t, ctx, pool, `INSERT INTO buckets (backend_id, name, owner_tenant_id)
		 SELECT sb.id, $2, $3 FROM storage_backends sb WHERE sb.name = $1`, newBackend, newBucket, f.tenantID)
	mustExec(t, ctx, pool, `UPDATE collections SET bucket_id = (SELECT id FROM buckets WHERE name = $2) WHERE id = $1`,
		f.collectionID, newBucket)
	mustExec(t, ctx, pool, `UPDATE storage_backends SET read_only = TRUE WHERE name = $1`, anchorBackend)

	got, err := repo.GetSession(ctx, sess.UploadID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.BackendID != anchorBackend || got.Bucket != anchorBucket {
		t.Fatalf("GetSession = %q/%q, want the anchored %q/%q", got.BackendID, got.Bucket, anchorBackend, anchorBucket)
	}
	want := bucketgate.State{Enabled: true, ReadOnly: true, ProvisionState: bucketgate.ProvisionReady}
	if got.BucketState != want {
		t.Fatalf("GetSession state = %+v, want the anchored bucket's %+v", got.BucketState, want)
	}
	if got.BucketConstraints.MaxParts != anchoredMaxParts {
		t.Errorf("GetSession max parts = %d, want the anchored bucket's %d", got.BucketConstraints.MaxParts, anchoredMaxParts)
	}
	if err := bucketgate.Check(bucketgate.BytesWrite, got.BucketState); !errors.Is(err, bucketgate.ErrBackendReadOnly) {
		t.Errorf("a write on the session = %v, want %v", err, bucketgate.ErrBackendReadOnly)
	}
	if err := bucketgate.Check(bucketgate.Read, got.BucketState); err != nil {
		t.Errorf("an abort on the session = %v, want it let through", err)
	}

	// Disabled, the anchored backend refuses the abort too.
	mustExec(t, ctx, pool, `UPDATE storage_backends SET enabled = FALSE WHERE name = $1`, anchorBackend)
	got, err = repo.GetSession(ctx, sess.UploadID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if err := bucketgate.Check(bucketgate.Read, got.BucketState); !errors.Is(err, bucketgate.ErrBackendDisabled) {
		t.Errorf("an abort on a disabled backend = %v, want %v", err, bucketgate.ErrBackendDisabled)
	}
}
