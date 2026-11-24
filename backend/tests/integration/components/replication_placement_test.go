//go:build integration

package components

import (
	"context"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// bucketID reads a bucket's id by name on the fixture's backend.
func (f publicMigrationFixture) bucketID(t *testing.T, ctx context.Context, bucket string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := f.pool.QueryRow(ctx, `SELECT b.id FROM buckets b JOIN storage_backends sb ON sb.id = b.backend_id
		WHERE sb.name = $1 AND b.name = $2`, f.backend, bucket).Scan(&id); err != nil {
		t.Fatalf("read bucket %s: %v", bucket, err)
	}
	return id
}

// queuedFor reads the bucket the object is queued for replication from;
// invalid when it is queued nowhere.
func (f publicMigrationFixture) queuedFor(t *testing.T, ctx context.Context, id uuid.UUID) []pgtype.UUID {
	t.Helper()
	rows, err := f.pool.Query(ctx, `SELECT source_bucket_id FROM object_replicas WHERE object_id = $1 ORDER BY updated_at`, id)
	if err != nil {
		t.Fatalf("read replicas of %s: %v", id, err)
	}
	defer rows.Close()
	var out []pgtype.UUID
	for rows.Next() {
		var b pgtype.UUID
		if err := rows.Scan(&b); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, b)
	}
	return out
}

// Replication copies the objects whose bytes are in a bucket (ADR-0051): an
// object is queued for the bucket it is placed in, reports that bucket to the
// worker, and is queued again when it moves; a collection rebound elsewhere
// moves nothing and queues nothing.
func TestReplicationFollowsThePlacement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, placementCollection, f.private, false)
	mustExec(t, ctx, f.pool, `UPDATE buckets SET replication_enabled = true WHERE name = $1`, f.private)
	id := f.uploadAndPromote(t, ctx, placementCollection, "replicated.txt")
	private := f.bucketID(t, ctx, f.private)
	if got := f.queuedFor(t, ctx, id); len(got) != 1 || got[0] != private {
		t.Fatalf("queued for %v after the promote, want the bucket it is placed in", got)
	}

	// The collection now points elsewhere; the object's bytes stay where
	// they are, and so does what the worker reads.
	f.rebindTo(t, ctx, placementCollection, f.dedicated)
	rows, err := sqlc.New(f.pool).GetReplicaObjects(ctx, []pgtype.UUID{{Bytes: id, Valid: true}})
	if err != nil || len(rows) != 1 {
		t.Fatalf("GetReplicaObjects: %d rows, %v", len(rows), err)
	}
	if rows[0].BucketID != private {
		t.Fatalf("worker reads the object in %v, want the bucket it is placed in %v", rows[0].BucketID, private)
	}
	mustExec(t, ctx, f.pool, `DELETE FROM object_replicas WHERE object_id = $1`, id)
	mustExec(t, ctx, f.pool, `DELETE FROM replication_seeds`)
	// obj-1, which the fixture placed there, and the promoted object.
	const placedInPrivate = 2
	if n, err := sqlc.New(f.pool).SeedReplication(ctx, private); err != nil || n != placedInPrivate {
		t.Fatalf("SeedReplication of the bucket the objects are placed in queued %d, %v; want %d", n, err, placedInPrivate)
	}

	// Moving the bytes queues the object for the bucket it moved to.
	dedicated := f.bucketID(t, ctx, f.dedicated)
	mustExec(t, ctx, f.pool, `UPDATE buckets SET replication_enabled = true WHERE name = $1`, f.dedicated)
	mustExec(t, ctx, f.pool, `UPDATE objects SET placement_bucket_id = $2 WHERE id = $1`, id, dedicated)
	got := f.queuedFor(t, ctx, id)
	if len(got) != 2 || got[1] != dedicated {
		t.Fatalf("queued for %v after the move, want the bucket it moved to last", got)
	}
}
