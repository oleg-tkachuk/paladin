//go:build integration

// How a folder listing (089) scales with the collection under it. Not part of
// the suite: seeding millions of objects takes minutes, so it runs only when
// folderScaleEnv names the collection's size.
//
//	PALADIN_FOLDER_SCALE=1000000 go test -tags=integration -run=TestFolderListingScale \
//	    -timeout=60m -v ./tests/integration/components/...
package components

import (
	"context"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// folderScaleEnv names the number of objects to seed in one collection.
const folderScaleEnv = "PALADIN_FOLDER_SCALE"

// The seeded tree: rootFolders folders at the root, each holding subFolders
// folders, the objects spread evenly over them. Under MaxCommonPrefixes, so
// no listing is cut.
const (
	rootFolders     = 500
	subFolders      = 50
	scaleCollection = "scale"
	// timedRuns listings are timed after one warm-up; their median is logged.
	timedRuns = 5
	// writeBatch objects are inserted with the walk's index and again
	// without it, to show what the index costs every upload.
	writeBatch = 100000
	walkIndex  = "idx_objects_collection_path_c"
)

func TestFolderListingScale(t *testing.T) {
	raw := os.Getenv(folderScaleEnv)
	if raw == "" {
		t.Skipf("%s is not set", folderScaleEnv)
	}
	objects, err := strconv.Atoi(raw)
	if err != nil || objects < rootFolders*subFolders {
		t.Fatalf("%s=%q: want a count of at least %d", folderScaleEnv, raw, rootFolders*subFolders)
	}
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	seedBucketRow(t, ctx, admin)
	seedCollectionFor(t, ctx, admin, tenant, scaleCollection)

	start := time.Now()
	mustExec(t, ctx, admin, `
		INSERT INTO objects (tenant_id, collection_id, path, state, content_type)
		SELECT $1, c.id,
		       'd' || (i % $3) || '/s' || ((i / $3) % $4) || '/o' || i,
		       'AVAILABLE', 'text/plain'
		  FROM collections c, generate_series(1, $2) AS i
		 WHERE c.tenant_id = $1 AND c.name = $5`,
		tenant, objects, rootFolders, subFolders, scaleCollection)
	mustExec(t, ctx, admin, `VACUUM ANALYZE objects`)
	var size string
	if err := admin.QueryRow(ctx, `SELECT pg_size_pretty(pg_total_relation_size('objects'))`).Scan(&size); err != nil {
		t.Fatalf("objects size: %v", err)
	}
	t.Logf("seeded %d objects in %s; objects with its indexes: %s", objects, time.Since(start).Round(time.Second), size)

	pool := rlsPool(t, ctx, admin)
	repo := adapters.NewObjectRepo(sqlc.New(pool), pool)
	as := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenant})
	for _, tc := range []struct {
		name, prefix string
		want         int
	}{
		{"root", "", rootFolders},
		{"one folder down", "d7/", subFolders},
	} {
		list := func() []string {
			t.Helper()
			got, truncated, err := repo.ListCommonPrefixes(as, objecth.ListPrefixesArgs{
				TenantID: tenant, Collection: scaleCollection, Prefix: tc.prefix, Delimiter: folderDelimiter,
			})
			if err != nil || truncated {
				t.Fatalf("%s: truncated=%v err=%v", tc.name, truncated, err)
			}
			return got
		}
		if got := list(); len(got) != tc.want {
			t.Fatalf("%s: %d folders, want %d", tc.name, len(got), tc.want)
		}
		runs := make([]time.Duration, 0, timedRuns)
		for range timedRuns {
			begin := time.Now()
			list()
			runs = append(runs, time.Since(begin))
		}
		slices.Sort(runs)
		t.Logf("%s (%q): median %s, min %s, max %s over %d objects",
			tc.name, tc.prefix, runs[len(runs)/2], runs[0], runs[len(runs)-1], objects)
	}

	insertBatch := func(from int) time.Duration {
		t.Helper()
		begin := time.Now()
		mustExec(t, ctx, admin, `
			INSERT INTO objects (tenant_id, collection_id, path, state, content_type)
			SELECT $1, c.id, 'w/o' || i, 'AVAILABLE', 'text/plain'
			  FROM collections c, generate_series($2::int, $2::int + $3::int - 1) AS i
			 WHERE c.tenant_id = $1 AND c.name = $4`,
			tenant, from, writeBatch, scaleCollection)
		return time.Since(begin)
	}
	// With, without, and with again, so neither side gains from running
	// first or last.
	var indexDef string
	if err := admin.QueryRow(ctx, `SELECT pg_get_indexdef($1::regclass)`, walkIndex).Scan(&indexDef); err != nil {
		t.Fatalf("%s: %v", walkIndex, err)
	}
	with := insertBatch(0)
	mustExec(t, ctx, admin, `DROP INDEX `+walkIndex)
	without := insertBatch(writeBatch)
	mustExec(t, ctx, admin, indexDef)
	withAgain := insertBatch(2 * writeBatch)
	t.Logf("%d inserts: %s and %s with %s, %s without", writeBatch,
		with.Round(time.Millisecond), withAgain.Round(time.Millisecond), walkIndex, without.Round(time.Millisecond))
}
