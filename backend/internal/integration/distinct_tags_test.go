//go:build integration

package integration

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// TestListDistinctTags proves the ObjectRepo.ListDistinctTags scan that backs
// the tag-facet filter: it aggregates distinct tag key→values across an
// Collection's live objects, sorts each value list, and excludes DELETED rows.
func TestListDistinctTags(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	insert := func(state, tags string) {
		t.Helper()
		mustExec(t, ctx, pool,
			`INSERT INTO objects (id, tenant_id, collection_id, path, state, content_type, checksum_algorithm, tags)
			 SELECT $1, $2, c.id, $4, $5, 'application/octet-stream', 0, $6::jsonb FROM collections c
			  WHERE c.tenant_id = $2 AND c.name = $3`,
			uuid.Must(uuid.NewV7()), f.tenantID, f.collection,
			"k-"+uuid.NewString()[:8], state, tags)
	}
	insert("AVAILABLE", `{"env":"prod","team":"data"}`)
	insert("AVAILABLE", `{"env":"staging","team":"data"}`)
	insert("AVAILABLE", `{"env":"prod"}`)
	insert("DELETED", `{"env":"ghost"}`) // must be excluded (state DELETED)

	repo := adapters.NewObjectRepo(sqlc.New(pool), pool)
	got, err := repo.ListDistinctTags(ctx, f.tenantID, f.collection)
	if err != nil {
		t.Fatalf("ListDistinctTags: %v", err)
	}
	want := map[string][]string{
		"env":  {"prod", "staging"}, // distinct + sorted, no "ghost"
		"team": {"data"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("distinct tags = %v, want %v", got, want)
	}
}
