//go:build integration

package integration

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// TestListDistinctTags proves the ObjectRepo.ListDistinctTags scan that backs
// the tag-facet filter: it aggregates distinct tag key→values across an
// Collection's live objects, sorts each value list, and excludes DELETED rows.
// setupDistinctTags brings up a pool + fixture for the tag-facet tests.
func setupDistinctTags(t *testing.T) (context.Context, *pgxpool.Pool, fixture) {
	t.Helper()
	ctx := context.Background()
	pool := startPostgres(t)
	return ctx, pool, seedFixture(t, ctx, pool)
}

// insertTaggedObject adds one object carrying the given JSON tag map.
func insertTaggedObject(t *testing.T, ctx context.Context, pool *pgxpool.Pool, f fixture, state, tags string) {
	t.Helper()
	mustExec(t, ctx, pool,
		`INSERT INTO objects (id, tenant_id, collection_id, path, state, content_type, checksum_algorithm, tags)
		 SELECT $1, $2, c.id, $4, $5, 'application/octet-stream', 0, $6::jsonb FROM collections c
		  WHERE c.tenant_id = $2 AND c.name = $3`,
		uuid.Must(uuid.NewV7()), f.tenantID, f.collection,
		"k-"+uuid.NewString()[:8], state, tags)
}

func TestListDistinctTags(t *testing.T) {
	ctx, pool, f := setupDistinctTags(t)

	insert := func(state, tags string) {
		t.Helper()
		insertTaggedObject(t, ctx, pool, f, state, tags)
	}
	insert("AVAILABLE", `{"env":"prod","team":"data"}`)
	insert("AVAILABLE", `{"env":"staging","team":"data"}`)
	insert("AVAILABLE", `{"env":"prod"}`)
	insert("DELETED", `{"env":"ghost"}`) // must be excluded (state DELETED)

	repo := adapters.NewObjectRepo(sqlc.New(pool), pool)
	page, err := repo.ListDistinctTags(ctx, f.tenantID, f.collection, "", 50, 100)
	if err != nil {
		t.Fatalf("ListDistinctTags: %v", err)
	}
	want := map[string][]string{
		"env":  {"prod", "staging"}, // distinct + sorted, no "ghost"
		"team": {"data"},
	}
	if !reflect.DeepEqual(page.Values, want) {
		t.Fatalf("distinct tags = %v, want %v", page.Values, want)
	}
	if !reflect.DeepEqual(page.Keys, []string{"env", "team"}) {
		t.Fatalf("keys = %v, want [env team] in ascending order", page.Keys)
	}
	if page.NextKey != "" {
		t.Errorf("NextKey = %q, want empty — two keys fit in a page of 50", page.NextKey)
	}
	for k, tr := range page.Truncated {
		if tr {
			t.Errorf("key %q marked truncated, but its values fit under the cap", k)
		}
	}
}

// TestListDistinctTagsPaging covers the two bounds the rewrite added: the key
// cursor and the per-key value cap. Both were unbounded before — one response
// carried every key and every value, and nothing in the response said so.
func TestListDistinctTagsPaging(t *testing.T) {
	ctx, pool, f := setupDistinctTags(t)

	// One key with more values than the cap, plus enough keys to page.
	for i := 0; i < 5; i++ {
		insertTaggedObject(t, ctx, pool, f, "AVAILABLE",
			fmt.Sprintf(`{"colour":"c%02d","k%d":"v"}`, i, i))
	}

	repo := adapters.NewObjectRepo(sqlc.New(pool), pool)

	t.Run("key cursor walks every key exactly once", func(t *testing.T) {
		seen := []string{}
		cursor := ""
		for range 10 { // bounded so a cursor bug cannot hang the suite
			page, err := repo.ListDistinctTags(ctx, f.tenantID, f.collection, cursor, 2, 100)
			if err != nil {
				t.Fatalf("page after %q: %v", cursor, err)
			}
			if len(page.Keys) > 2 {
				t.Fatalf("page returned %d keys, want at most 2", len(page.Keys))
			}
			seen = append(seen, page.Keys...)
			if page.NextKey == "" {
				break
			}
			cursor = page.NextKey
		}
		want := []string{"colour", "k0", "k1", "k2", "k3", "k4"}
		if !reflect.DeepEqual(seen, want) {
			t.Fatalf("paged keys = %v, want %v", seen, want)
		}
	})

	t.Run("value cap truncates and says so", func(t *testing.T) {
		page, err := repo.ListDistinctTags(ctx, f.tenantID, f.collection, "", 50, 2)
		if err != nil {
			t.Fatalf("ListDistinctTags: %v", err)
		}
		// "colour" has five distinct values; the cap is two.
		if got := len(page.Values["colour"]); got != 2 {
			t.Errorf("colour values = %d, want 2 (the cap)", got)
		}
		if !page.Truncated["colour"] {
			t.Error("colour has more values than the cap but is not marked truncated")
		}
		// "k0" has exactly one value — under the cap, so not truncated.
		if page.Truncated["k0"] {
			t.Error("k0 fits under the cap but is marked truncated")
		}
	})
}
