//go:build integration

package components

import (
	"context"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// trash moves collection/key to the trash, as a soft delete does.
func (f publicMigrationFixture) trash(t *testing.T, ctx context.Context, collection, key string) {
	t.Helper()
	mustExec(t, ctx, f.pool, `UPDATE objects o SET state = 'DELETED', terminated_at = now()
		FROM collections c WHERE c.id = o.collection_id
		  AND o.tenant_id = $1 AND c.name = $2 AND o.path = $3`, f.tenant, collection, key)
}

func (f publicMigrationFixture) objectID(t *testing.T, ctx context.Context, collection, key string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT o.id FROM objects o JOIN collections c ON c.id = o.collection_id
		WHERE o.tenant_id = $1 AND c.name = $2 AND o.path = $3`, f.tenant, collection, key).Scan(&id); err != nil {
		t.Fatalf("read id of %s/%s: %v", collection, key, err)
	}
	return id
}

// A trashed object used to stay behind: the migration copied live objects
// only, so after it the collection was bound to a bucket without the trashed
// object's bytes, RestoreObject refused it, and the purge deleted from the
// target bytes that were never there, orphaning them in the source. The
// trash now moves with the tenant, and is restorable from the new bucket.
func TestStorageMigrationMovesTheTrash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	f.upload(t, ctx, "docs", "trashed-1", 9)
	f.trash(t, ctx, "docs", "trashed-1")
	// Its bytes deleted straight from the bucket: trashed, nothing to move.
	f.upload(t, ctx, "docs", "bytes-gone", 4)
	f.trash(t, ctx, "docs", "bytes-gone")
	if err := f.store.DeleteObject(ctx, f.loc(f.private, "docs", "bytes-gone")); err != nil {
		t.Fatalf("delete bytes: %v", err)
	}
	if err := f.start(t, ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	if state, reason := f.drive(t, ctx); state != worker.MigStateCleaned {
		t.Fatalf("migration left at %s (%q), want cleaned", state, reason)
	}
	if size, ok := f.store.sizeOf(f.loc(f.dedicated, "docs", "trashed-1")); !ok || size != 9 {
		t.Errorf("trashed object in the dedicated bucket: size %d, present %v; want 9 bytes", size, ok)
	}
	if f.store.has(f.loc(f.private, "docs", "trashed-1")) {
		t.Error("cleanup left the trashed object's old copy in the source")
	}
	if f.store.has(f.loc(f.dedicated, "docs", "bytes-gone")) {
		t.Error("an object with no bytes left has bytes in the dedicated bucket")
	}
	for _, key := range []string{"trashed-1", "bytes-gone"} {
		if got := f.objectState(t, ctx, "docs", key); got != "DELETED" {
			t.Errorf("docs/%s is %s after the migration, want still in the trash", key, got)
		}
	}

	// The restore: the row comes out of the trash, and its bytes are in the
	// bucket the collection is bound to now — what RestoreObject HEADs.
	rows, err := sqlc.New(f.pool).RestoreObject(ctx,
		pgtype.UUID{Bytes: f.tenant, Valid: true}, pgtype.UUID{Bytes: f.objectID(t, ctx, "docs", "trashed-1"), Valid: true})
	if err != nil || rows != 1 {
		t.Fatalf("restore = %d rows, %v; want 1", rows, err)
	}
	bound := f.boundTo(t, ctx, "docs")
	if size, ok := f.store.sizeOf(f.loc(bound, "docs", "trashed-1")); bound != f.dedicated || !ok || size != 9 {
		t.Errorf("restored object: bound to %s, bytes there %v (%d); want 9 bytes in %s", bound, ok, size, f.dedicated)
	}
}
