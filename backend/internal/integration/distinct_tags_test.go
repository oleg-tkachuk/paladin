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
// ObjectKey's live objects, sorts each value list, and excludes DELETED rows.
func TestListDistinctTags(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	insert := func(state, tags string) {
		t.Helper()
		mustExec(t, ctx, pool,
			`INSERT INTO objects (object_id, tenant_id, object_key, key, state, content_type, checksum_algorithm, tags)
			 VALUES ($1, $2, $3, $4, $5, 'application/octet-stream', 0, $6::jsonb)`,
			uuid.Must(uuid.NewV7()), f.tenantID, f.objectKey,
			"k-"+uuid.NewString()[:8], state, tags)
	}
	insert("AVAILABLE", `{"env":"prod","team":"data"}`)
	insert("AVAILABLE", `{"env":"staging","team":"data"}`)
	insert("AVAILABLE", `{"env":"prod"}`)
	insert("DELETED", `{"env":"ghost"}`) // must be excluded (state DELETED)

	repo := adapters.NewObjectRepo(sqlc.New(pool), pool)
	got, err := repo.ListDistinctTags(ctx, f.tenantID, f.objectKey)
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
