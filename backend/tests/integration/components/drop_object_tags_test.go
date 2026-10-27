//go:build integration

package components

import (
	"context"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// beforeObjectTagsDrop is the last migration with object_tags; 071 drops it.
const beforeObjectTagsDrop = 70

// 071 drops the unused object_tags table, and refuses to while it holds a
// row: nothing reads them, but dropping someone's data is a decision, not a
// side effect of a migration. Down brings the table back.
func TestDropObjectTags(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })
	exists := func() bool {
		t.Helper()
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass('public.object_tags') IS NOT NULL`).Scan(&ok); err != nil {
			t.Fatalf("to_regclass: %v", err)
		}
		return ok
	}

	if exists() {
		t.Fatal("object_tags exists after every migration")
	}
	if err := goose.DownTo(db, ".", beforeObjectTagsDrop); err != nil {
		t.Fatalf("migrate down to %d: %v", beforeObjectTagsDrop, err)
	}
	if !exists() {
		t.Fatal("down did not bring object_tags back")
	}

	tenant, _ := mkTenant(t, ctx, pool, "shared")
	mustExec(t, ctx, pool, `INSERT INTO object_tags (id, tenant_id, slug) VALUES ($1, $2, 'kept')`, uuid.New(), tenant)
	if err := goose.Up(db, "."); err == nil {
		t.Fatal("071 dropped object_tags while it held a row")
	}
	if !exists() {
		t.Fatal("the refused migration dropped the table anyway")
	}

	mustExec(t, ctx, pool, `DELETE FROM object_tags`)
	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("migrate up over an empty object_tags: %v", err)
	}
	if exists() {
		t.Error("object_tags survived 071")
	}
}
