//go:build integration

package components

import (
	"context"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// beforeOperationProgress is the last migration that kept progress in
// metadata; 073 gives it a column.
const beforeOperationProgress = 72

// 073 moves a progress snapshot out of metadata into its own column, and
// leaves metadata that holds anything else — the executor's arguments — where
// it is. Down puts a snapshot back where the old code reads it.
func TestOperationProgressColumnMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })

	if err := goose.DownTo(db, ".", beforeOperationProgress); err != nil {
		t.Fatalf("migrate down to %d: %v", beforeOperationProgress, err)
	}
	tenant, _ := mkTenant(t, ctx, pool, "shared")
	snapshot, args, extraKey, notJSON, empty := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for id, meta := range map[uuid.UUID][]byte{
		snapshot: []byte(`{"processed":7,"total":9}`),
		args:     []byte(`{"object_ids":["a"]}`),
		extraKey: []byte(`{"processed":1,"total":2,"collection":"k"}`),
		notJSON:  []byte(`not json`),
		empty:    nil,
	} {
		mustExec(t, ctx, pool,
			`INSERT INTO operations (id, tenant_id, type, state, metadata) VALUES ($1, $2, 'BatchDelete', 'RUNNING', $3)`,
			id, tenant, meta)
	}

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	read := func(id uuid.UUID) (meta []byte, progress *string) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT metadata, progress::text FROM operations WHERE id = $1`, id).
			Scan(&meta, &progress); err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		return meta, progress
	}

	if meta, progress := read(snapshot); meta != nil || progress == nil || *progress != `{"total": 9, "processed": 7}` {
		t.Errorf("snapshot row: metadata = %q, progress = %v; want the snapshot moved", meta, deref(progress))
	}
	for name, tc := range map[string]struct {
		id   uuid.UUID
		meta string
	}{
		"arguments":           {args, `{"object_ids":["a"]}`},
		"snapshot plus a key": {extraKey, `{"processed":1,"total":2,"collection":"k"}`},
		"not JSON":            {notJSON, `not json`},
		"no metadata":         {empty, ``},
	} {
		if meta, progress := read(tc.id); string(meta) != tc.meta || progress != nil {
			t.Errorf("%s: metadata = %q, progress = %v; want metadata untouched and no progress", name, meta, deref(progress))
		}
	}

	if err := goose.DownTo(db, ".", beforeOperationProgress); err != nil {
		t.Fatalf("migrate down again: %v", err)
	}
	var meta []byte
	if err := pool.QueryRow(ctx, `SELECT metadata FROM operations WHERE id = $1`, snapshot).Scan(&meta); err != nil {
		t.Fatalf("read after down: %v", err)
	}
	if string(meta) != `{"total": 9, "processed": 7}` {
		t.Errorf("down left metadata = %q, want the snapshot back", meta)
	}
	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
