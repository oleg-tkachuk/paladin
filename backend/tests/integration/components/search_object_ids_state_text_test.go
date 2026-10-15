//go:build integration

package components

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// beforeStateAsText is the last migration with search_object_ids taking an
// object_state; 078 takes the state as text.
const beforeStateAsText = 77

// The state parameter's type, as the catalogue has it.
const (
	stateParamEnum = "object_state"
	stateParamText = "text"
)

// 078 changes the function's signature, so Up drops the old one rather than
// leaving an overload beside it, and Down restores it the same way.
func TestSearchObjectIDsStateAsText(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })
	stateTypes := func() []string {
		t.Helper()
		rows, err := pool.Query(ctx, `
			SELECT format_type(p.proargtypes[2], NULL)
			  FROM pg_proc p
			 WHERE p.proname = 'search_object_ids'
			 ORDER BY 1`)
		if err != nil {
			t.Fatalf("pg_proc: %v", err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatalf("scan: %v", err)
			}
			out = append(out, s)
		}
		return out
	}
	want := func(phase, typ string) {
		t.Helper()
		if got := stateTypes(); len(got) != 1 || got[0] != typ {
			t.Fatalf("%s: search_object_ids state parameters %v, want exactly one %s", phase, got, typ)
		}
	}

	want("up", stateParamText)
	if err := goose.DownTo(db, ".", beforeStateAsText); err != nil {
		t.Fatalf("migrate down to %d: %v", beforeStateAsText, err)
	}
	want("down", stateParamEnum)
	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	want("up again", stateParamText)
}
