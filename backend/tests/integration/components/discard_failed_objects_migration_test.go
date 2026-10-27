//go:build integration

package components

import (
	"context"
	"slices"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// beforeDiscardingFailed is the last migration under which an object could be
// kept as FAILED; 079 deletes the rows kept until then and drops the label.
const beforeDiscardingFailed = 78

// Migrating a database that still holds FAILED objects removes them, frees
// their keys, and leaves every other object where it was.
func TestFailedObjectsAreDiscardedByTheMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgresAt(t, beforeDiscardingFailed)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })
	f := seedFixture(t, ctx, pool)

	insert := func(path, state string) uuid.UUID {
		t.Helper()
		id := uuid.NewV7()
		mustExec(t, ctx, pool, `
			INSERT INTO objects (id, tenant_id, collection_id, path, state,
			                     content_type, checksum_algorithm)
			SELECT $1, $2, c.id, $4, $5::object_state, 'text/plain', 0
			  FROM collections c WHERE c.tenant_id = $2 AND c.name = $3`,
			id, f.tenantID, f.collection, path, state)
		return id
	}
	failed := insert("failed.txt", "FAILED")
	kept := []uuid.UUID{
		insert("pending.txt", "PENDING"),
		insert("available.txt", "AVAILABLE"),
		insert("deleted.txt", "DELETED"),
	}

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	// The type was replaced, and a connection that prepared statements against
	// the old one fails them ("cache lookup failed for type"); a deploy starts
	// its pods after the migration, on fresh connections.
	pool.Reset()

	if !objectGone(t, ctx, pool, failed) {
		t.Error("the FAILED object survived the migration")
	}
	for _, id := range kept {
		if objectGone(t, ctx, pool, id) {
			t.Errorf("object %s was not FAILED and was removed", id)
		}
	}
	// Its key is free again.
	insert("failed.txt", "PENDING")

	// And the label is gone from object_state.
	var labels []string
	if err := pool.QueryRow(ctx,
		`SELECT enum_range(NULL::object_state)::text[]`).Scan(&labels); err != nil {
		t.Fatalf("read labels: %v", err)
	}
	if want := []string{"PENDING", "AVAILABLE", "DELETED"}; !slices.Equal(labels, want) {
		t.Errorf("object_state labels = %v, want %v", labels, want)
	}
}
