//go:build integration

package components

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	objecth "github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/migstate"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/tests/objectrow"
)

// The CHECKs migration 132 adds and 133 validates (ADR-0051).
const (
	placedPastPendingCheck = "objects_placed_past_pending"
	pendingAnchoredCheck   = "objects_pending_anchored"
	uploadBucketFK         = "objects_upload_bucket_fk"
)

// sqlStateCheckViolation is Postgres' check_violation.
const sqlStateCheckViolation = "23514"

// beforePlacementRequired is the last migration under which an object row
// could be placed nowhere, or PENDING with no anchor.
const beforePlacementRequired = 131

// refusedBy fails the test unless err is a check violation of constraint.
func refusedBy(t *testing.T, err error, constraint string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != sqlStateCheckViolation || pgErr.ConstraintName != constraint {
		t.Fatalf("err = %v, want a check violation of %s", err, constraint)
	}
}

// The schema refuses a row past PENDING that is placed nowhere, and a PENDING
// row anchored to no bucket — on insert and on update alike.
func TestObjectRowsArePlacedAndAnchored(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	insert := func(state string, anchored, placed bool) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO objects (tenant_id, collection_id, path, state, content_type,
			                     upload_bucket_id, placement_bucket_id)
			SELECT c.tenant_id, c.id, $2, $3::object_state, 'text/plain',
			       CASE WHEN $4 THEN c.bucket_id END, CASE WHEN $5 THEN c.bucket_id END
			  FROM collections c WHERE c.id = $1`,
			f.collectionID, "k-"+uuid.New().String()[:8], state, anchored, placed)
		return err
	}
	for _, state := range []string{objectrow.StateAvailable, objectrow.StateDeleted} {
		refusedBy(t, insert(state, true, false), placedPastPendingCheck)
		if err := insert(state, false, true); err != nil {
			t.Errorf("a placed %s row was refused: %v", state, err)
		}
	}
	refusedBy(t, insert(objectrow.StatePending, false, false), pendingAnchoredCheck)
	if err := insert(objectrow.StatePending, true, false); err != nil {
		t.Errorf("an anchored PENDING row was refused: %v", err)
	}

	available := seedObject(t, ctx, pool, objectrow.Row{TenantID: f.tenantID, CollectionID: f.collectionID, Path: "placed"})
	_, err := pool.Exec(ctx, `UPDATE objects SET placement_bucket_id = NULL WHERE id = $1`, available)
	refusedBy(t, err, placedPastPendingCheck)
	pending := seedObject(t, ctx, pool, objectrow.Row{
		TenantID: f.tenantID, CollectionID: f.collectionID, Path: "anchored", State: objectrow.StatePending,
	})
	_, err = pool.Exec(ctx, `UPDATE objects SET upload_bucket_id = NULL WHERE id = $1`, pending)
	refusedBy(t, err, pendingAnchoredCheck)
	// A promoted object's anchor is the settle's to clear.
	if _, err := pool.Exec(ctx, `UPDATE objects SET upload_bucket_id = NULL WHERE id = $1`, available); err != nil {
		t.Errorf("settling a promoted row's anchor was refused: %v", err)
	}
}

// A bucket an upload is anchored to is held by it: DeleteBucket counts the
// row and refuses, where the foreign key used to clear the anchor.
func TestABucketAnUploadIsAnchoredToIsHeld(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	seedObject(t, ctx, pool, objectrow.Row{
		TenantID: f.tenantID, CollectionID: f.collectionID, Path: "uploading", State: objectrow.StatePending,
	})
	// The collection moves on, as an operator's rebind leaves it: only the
	// upload's anchor still names the bucket.
	next := "bkt-next-" + uuid.New().String()[:8]
	mustExec(t, ctx, pool, `INSERT INTO buckets (backend_id, name, owner_tenant_id)
		SELECT sb.id, $2, $3 FROM storage_backends sb WHERE sb.name = $1`, f.backend, next, f.tenantID)
	mustExec(t, ctx, pool, `UPDATE collections SET bucket_id = (
		SELECT b.id FROM buckets b JOIN storage_backends sb ON sb.id = b.backend_id
		 WHERE sb.name = $2 AND b.name = $3) WHERE id = $1`, f.collectionID, f.backend, next)

	refs, err := sqlc.New(pool).CountBucketReferences(ctx, f.backend, f.bucket, migstate.HoldsBuckets())
	if err != nil {
		t.Fatalf("CountBucketReferences: %v", err)
	}
	held := false
	for _, r := range refs {
		if r.Relation == "objects" && r.Count > 0 {
			held = true
		}
	}
	if !held {
		t.Errorf("references = %+v, want the anchored upload counted under objects", refs)
	}
	// The repository's delete, which the count would not stop on a race:
	// the foreign key refuses it, told by what holds the bucket.
	err = adapters.NewBucketRepoV2(sqlc.New(pool), pool).Delete(ctx, f.backend, f.bucket, 0)
	if !errors.Is(err, admindomain.ErrConflict) {
		t.Fatalf("Delete of the anchor's bucket = %v, want ErrConflict", err)
	}
	if want := `conflict: bucket "` + f.bucket + `": held by objects`; !strings.HasPrefix(err.Error(), want) {
		t.Errorf("the refusal must name what holds the bucket.\ngot:  %v\nwant: %v…", err, want)
	}
	assertNoSchemaNames(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM buckets b USING storage_backends sb
		WHERE sb.id = b.backend_id AND sb.name = $1 AND b.name = $2`, f.backend, f.bucket)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != uploadBucketFK {
		t.Errorf("deleting the anchor's bucket = %v, want it refused by %s", err, uploadBucketFK)
	}
}

// A batch reads its objects by id, and a permanent delete of a PENDING one
// owes the bucket its URL is signed for: the read carries the anchor as well
// as the placement, so the delete never needs the collection's binding.
func TestFindByIDsCarriesAnchorAndPlacement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	pending := seedObject(t, ctx, pool, objectrow.Row{
		TenantID: f.tenantID, CollectionID: f.collectionID, Path: "uploading", State: objectrow.StatePending,
	})
	placed := seedObject(t, ctx, pool, objectrow.Row{TenantID: f.tenantID, CollectionID: f.collectionID, Path: "placed"})

	objs, err := adapters.NewObjectRepo(sqlc.New(pool), pool).FindByIDs(ctx, f.tenantID, []uuid.UUID{pending, placed})
	if err != nil {
		t.Fatalf("FindByIDs: %v", err)
	}
	for _, o := range objs {
		ref, other := o.Stored, o.Upload
		if o.ObjectID == pending {
			ref, other = o.Upload, o.Stored
		}
		if ref == nil || ref.BackendID != f.backend || ref.Bucket != f.bucket || other != nil {
			t.Errorf("%s (%s): upload %+v, placement %+v; want only the one its state has, in %s", o.Key, o.State, o.Upload, o.Stored, f.bucket)
		}
	}
	if len(objs) != 2 {
		t.Fatalf("FindByIDs returned %d objects, want 2", len(objs))
	}
}

// The migration places every row a binary before it left placed nowhere, and
// anchors every PENDING row it left anchored nowhere, in the collection's
// bucket where their bytes are, without moving a resource_version; and both
// CHECKs end validated.
func TestPlacementRequiredMigrationBackfills(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgresAt(t, beforePlacementRequired)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })
	f := seedFixture(t, ctx, pool)

	legacy := map[string]uuid.UUID{}
	for _, state := range []string{objectrow.StatePending, objectrow.StateAvailable, objectrow.StateDeleted} {
		id := uuid.NewV7()
		mustExec(t, ctx, pool, `INSERT INTO objects (id, tenant_id, collection_id, path, state, content_type)
			VALUES ($1, $2, $3, $4, $5::object_state, 'text/plain')`,
			id, f.tenantID, f.collectionID, "legacy-"+state, state)
		legacy[state] = id
	}
	versions := map[uuid.UUID]int64{}
	for _, id := range legacy {
		var v int64
		if err := pool.QueryRow(ctx, `SELECT resource_version FROM objects WHERE id = $1`, id).Scan(&v); err != nil {
			t.Fatalf("read version: %v", err)
		}
		versions[id] = v
	}

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	var binding uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT bucket_id FROM collections WHERE id = $1`, f.collectionID).Scan(&binding); err != nil {
		t.Fatalf("read binding: %v", err)
	}
	for state, id := range legacy {
		var anchor, placement *uuid.UUID
		var version int64
		if err := pool.QueryRow(ctx, `SELECT upload_bucket_id, placement_bucket_id, resource_version
			FROM objects WHERE id = $1`, id).Scan(&anchor, &placement, &version); err != nil {
			t.Fatalf("read %s row: %v", state, err)
		}
		got := placement
		if state == objectrow.StatePending {
			got = anchor
		}
		if got == nil || *got != binding {
			t.Errorf("%s row: anchor %v, placement %v; want the collection's bucket %s", state, anchor, placement, binding)
		}
		if version != versions[id] {
			t.Errorf("%s row: resource_version %d → %d; the backfill is bookkeeping", state, versions[id], version)
		}
	}

	for _, name := range []string{placedPastPendingCheck, pendingAnchoredCheck, uploadBucketFK} {
		var validated bool
		if err := pool.QueryRow(ctx, `SELECT convalidated FROM pg_constraint
			WHERE conrelid = 'objects'::regclass AND conname = $1`, name).Scan(&validated); err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !validated {
			t.Errorf("%s is not validated", name)
		}
	}
}

// An upload is anchored to the bucket its URL is signed for; one that names
// none is refused before a row is written, not by the CHECK.
func TestCreateObjectRefusesAnUploadAnchoredNowhere(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	repo := adapters.NewObjectRepo(sqlc.New(pool), pool)
	args := objecth.CreateObjectArgs{
		TenantID: f.tenantID, Collection: f.collection, Key: "nowhere",
		PresignExpiresAt: time.Now().Add(time.Hour),
	}

	if _, err := repo.CreateObject(ctx, args); !errors.Is(err, adapters.ErrNoUploadBucket) {
		t.Fatalf("CreateObject naming no bucket = %v, want ErrNoUploadBucket", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM objects WHERE tenant_id = $1`, f.tenantID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("%d rows written for a refused upload, want none", n)
	}
	args.UploadBackendID, args.UploadBucket = f.backend, f.bucket
	if _, err := repo.CreateObject(ctx, args); err != nil {
		t.Errorf("CreateObject anchored to the binding: %v", err)
	}
}
