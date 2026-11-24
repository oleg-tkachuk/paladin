//go:build integration

package components

import (
	"context"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// placementCollection is the private collection the placement tests write.
const placementCollection = "docs"

// placedBucket reads the bucket an object's row places its bytes in
// (objects.placement_bucket_id, ADR-0051); "" when it places them nowhere.
func (f publicMigrationFixture) placedBucket(t *testing.T, ctx context.Context, id uuid.UUID) string {
	t.Helper()
	var bucket *string
	if err := f.pool.QueryRow(ctx, `SELECT b.name FROM objects o LEFT JOIN buckets b ON b.id = o.placement_bucket_id
		WHERE o.id = $1`, id).Scan(&bucket); err != nil {
		t.Fatalf("read placement of %s: %v", id, err)
	}
	if bucket == nil {
		return ""
	}
	return *bucket
}

// rebindTo points the collection at bucket, as an operator's
// BindCollectionToBucket does: no bytes move.
func (f publicMigrationFixture) rebindTo(t *testing.T, ctx context.Context, collection, bucket string) {
	t.Helper()
	mustExec(t, ctx, f.pool, `INSERT INTO buckets (backend_id, name, owner_tenant_id)
		SELECT sb.id, $2, $3 FROM storage_backends sb WHERE sb.name = $1
		ON CONFLICT (backend_id, name) DO NOTHING`, f.backend, bucket, f.tenant)
	mustExec(t, ctx, f.pool, `UPDATE collections SET bucket_id = (
			SELECT b.id FROM buckets b JOIN storage_backends sb ON sb.id = b.backend_id
			 WHERE sb.name = $3 AND b.name = $4)
		WHERE tenant_id = $1 AND name = $2`, f.tenant, collection, f.backend, bucket)
}

// uploadAndPromote registers an upload in collection and promotes it in the
// collection's bucket, as CompleteObject does.
func (f publicMigrationFixture) uploadAndPromote(t *testing.T, ctx context.Context, collection, key string) uuid.UUID {
	t.Helper()
	size := int64(7)
	obj, err := adapters.NewObjectRepo(sqlc.New(f.pool), f.pool).CreateObject(ctx, objecth.CreateObjectArgs{
		TenantID: f.tenant, Collection: collection, Key: key, ContentType: "text/plain", SizeBytes: &size,
		PresignExpiresAt: time.Now().Add(time.Hour), UploadBackendID: f.backend, UploadBucket: f.private,
	})
	if err != nil {
		t.Fatalf("create %s: %v", key, err)
	}
	if f.placedBucket(t, ctx, obj.ObjectID) != "" {
		t.Fatalf("a PENDING upload is placed before its bytes are confirmed")
	}
	changed, err := statemachine.New(f.pool).PromoteToAvailable(ctx, obj.ObjectID,
		&statemachine.Placement{BackendID: f.backend, Bucket: f.private}, "etag-1", size, "", "", statemachine.SourceRPC)
	if err != nil || !changed {
		t.Fatalf("promote %s: changed %v, %v", key, changed, err)
	}
	return obj.ObjectID
}

// The promote places an object where its bytes were confirmed, and every
// read of its bytes resolves that bucket after the collection is bound
// elsewhere — the single and batch reads, the list, the presign lookup, the
// workers' lookup by id and the trash's hard delete.
func TestObjectReadsFollowThePlacementNotTheBinding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, placementCollection, f.private, false)
	id := f.uploadAndPromote(t, ctx, placementCollection, "placed.txt")
	if got := f.placedBucket(t, ctx, id); got != f.private {
		t.Fatalf("promoted into %q, want %q", got, f.private)
	}

	f.rebindTo(t, ctx, placementCollection, f.dedicated)
	q := sqlc.New(f.pool)
	objects := adapters.NewObjectRepo(q, f.pool)

	obj, err := objects.FindByName(ctx, f.tenant, placementCollection, id.String())
	if err != nil {
		t.Fatalf("FindByName: %v", err)
	}
	assertStoredIn(t, "FindByName", obj.Stored, f.private)
	batch, err := objects.FindByIDs(ctx, f.tenant, []uuid.UUID{id})
	if err != nil || len(batch) != 1 {
		t.Fatalf("FindByIDs: %d rows, %v", len(batch), err)
	}
	assertStoredIn(t, "FindByIDs", batch[0].Stored, f.private)
	page, _, err := objects.ListObjects(ctx, objecth.ListObjectsArgs{TenantID: f.tenant, Collection: placementCollection, PageSize: 10})
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	for _, o := range page {
		if o.ObjectID == id {
			assertStoredIn(t, "ListObjects", o.Stored, f.private)
		}
	}
	ref, err := adapters.NewPresignRepo(q, f.pool).LookupObject(ctx, f.tenant, placementCollection, id)
	if err != nil {
		t.Fatalf("presign LookupObject: %v", err)
	}
	assertStoredIn(t, "presign LookupObject", ref.Stored, f.private)
	row, err := q.LookupObjectByID(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		t.Fatalf("LookupObjectByID: %v", err)
	}
	if row.BucketName != f.private {
		t.Errorf("LookupObjectByID: bucket %q, want %q", row.BucketName, f.private)
	}

	mustExec(t, ctx, f.pool, `UPDATE objects SET state = 'DELETED', terminated_at = now() - interval '1 hour' WHERE id = $1`, id)
	due, err := q.ListHardDeletable(ctx, pgtype.Timestamptz{Time: time.Now(), Valid: true}, nil, 1000)
	if err != nil {
		t.Fatalf("ListHardDeletable: %v", err)
	}
	found := false
	for _, d := range due {
		if d.ID.Bytes == id {
			found = true
			if d.BucketName != f.private {
				t.Errorf("ListHardDeletable: bucket %q, want %q", d.BucketName, f.private)
			}
		}
	}
	if !found {
		t.Fatal("ListHardDeletable did not list the trashed object")
	}
}

func assertStoredIn(t *testing.T, read string, ref *objecth.BucketRef, bucket string) {
	t.Helper()
	if ref == nil || ref.Bucket != bucket {
		t.Errorf("%s: stored in %+v, want %q", read, ref, bucket)
	}
}

// A storage migration that rebinds the tenant places every object it copied
// in the target with the binding, so nothing is read from the source it
// then cleans.
func TestStorageMigrationPlacesTheObjectsItMoves(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, placementCollection, f.private, false)
	id := f.uploadAndPromote(t, ctx, placementCollection, "moved.txt")
	f.store.put(worker.CopyLocation{BackendID: f.backend, TenantID: f.tenant, Bucket: f.private,
		Collection: placementCollection, Key: "moved.txt"}, 7)
	if err := f.start(t, ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if state, reason := f.drive(t, ctx); state != worker.MigStateCleaned {
		t.Fatalf("migration ended %s (%s), want %s", state, reason, worker.MigStateCleaned)
	}
	if got := f.placedBucket(t, ctx, id); got != f.dedicated {
		t.Fatalf("placed in %q after the migration, want the target %q", got, f.dedicated)
	}
}
