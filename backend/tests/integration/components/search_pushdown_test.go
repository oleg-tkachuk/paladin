//go:build integration

// The `search` field on backends and collections, held to the same contract as
// the bucket one in bucket_search_pushdown_test.go: SQL narrowing must never
// drop a row the authoritative CEL pass accepts.
//
// Three lists now define `search` three times over — cel.SearchText in Go, a
// clause per query in SQL, asciiLower in the console — and the only thing
// stopping them drifting is a test that runs both halves and compares. A drift
// does not throw; it returns a shorter list.
package components

import (
	"context"
	"fmt"
	"strings"

	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	objectkey "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/collectionh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	celpkg "github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// The adversarial rows, shared by both lists below: mixed case, a name with no
// display name, text Go and Postgres fold differently, and both LIKE
// metacharacters as DATA rather than as pattern.
var searchFixtures = []struct{ name, display string }{
	{"prod-logs", "Prod Logs"},
	{"staging", "STAGING AREA"},
	{"quiet", ""},
	{"istanbul-tr", "İstanbul"},
	{"uber-cache", "Über Cache"},
	{"pct", "100% done"},
	{"underscore", "a_b"},
	// For the separator probe below: a query of "b\nc" is contained in the
	// joined value and in neither column, so it must match nothing. Without
	// this pair the fixtures cannot tell chr(10) from chr(32) — a mutation
	// changing the separator survived until it was added.
	{"ab", "cd"},
}

var searchQueries = []string{
	"prod", "PROD", "Logs", "quiet", "istanbul", "İstanbul",
	"über", "ÜBER", "100%", "a_b", "b\nc", "", "nosuchthing",
	// The two columns the backend search covers beyond id and display name.
	// Without these the four-column join is untested and could shrink to two
	// without a failure.
	"eu-north", "s3.example",
}

func celFilter(q string) string {
	if q == "" {
		return ""
	}
	return "search.contains(" + celQuote(celpkg.SearchText(q)) + ")"
}

func subtestBackendSearch(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	repo := adapters.NewBackendRepoV2(sqlc.New(pool), pool)

	prefix := "srch-" + uuid.NewString()[:8]
	for _, f := range searchFixtures {
		mustExec(t, ctx, pool,
			`INSERT INTO storage_backends (name, kind, display_name, region, endpoint)
			 VALUES ($1, 's3-compatible', $2, $3, $4)`,
			prefix+"-"+f.name, f.display, "EU-North-1", "https://S3.Example/"+f.name)
	}

	all, _, err := repo.List(ctx, 1000, "", "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	for _, q := range searchQueries {
		t.Run(q, func(t *testing.T) {
			filter := celFilter(q)
			narrowed, _, err := repo.List(ctx, 1000, "", filter)
			if err != nil {
				t.Fatalf("list filtered: %v", err)
			}
			viaSQL := celBackends(t, filter, narrowed)
			viaAll := celBackends(t, filter, all)
			if len(viaSQL) != len(viaAll) {
				t.Errorf("pushdown changed the answer for %q: %d rows after SQL "+
					"narrowing, %d over every row — ListStorageBackends' search_like "+
					"clause and cel.SearchText disagree", q, len(viaSQL), len(viaAll))
			}
		})
	}
}

func celBackends(t *testing.T, filter string, in []admindomain.StorageBackend) []admindomain.StorageBackend {
	t.Helper()
	if filter == "" {
		return in
	}
	// A copy: FilterPage compacts in place and `in` is shared by every subtest.
	in = append([]admindomain.StorageBackend(nil), in...)
	out, err := celpkg.FilterPage(celpkg.NewEvaluator(), celpkg.StorageBackendSchema, filter, in,
		func(b admindomain.StorageBackend) map[string]any {
			return map[string]any{
				"backend_id":   b.BackendID,
				"display_name": b.DisplayName,
				"search":       celpkg.SearchText(b.BackendID, b.DisplayName, b.Region, b.Endpoint),
			}
		})
	if err != nil {
		t.Fatalf("cel filter %q: %v", filter, err)
	}
	return out
}

func subtestCollectionSearch(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	tenantID, _ := mkTenant(t, ctx, pool, "shared")

	const backendID = "srch-coll-be"
	mustExec(t, ctx, pool,
		`INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')
		 ON CONFLICT (name) DO NOTHING`, backendID)
	bucketID := uuid.New()
	mustExec(t, ctx, pool,
		`INSERT INTO buckets (id, backend_id, name, display_name)
		 SELECT $1, sb.id, 'srch-coll-bucket', 'b' FROM storage_backends sb WHERE sb.name = $2`,
		bucketID, backendID)

	for _, f := range searchFixtures {
		mustExec(t, ctx, pool,
			`INSERT INTO collections (id, tenant_id, name, display_name, bucket_id)
			 VALUES ($1, $2, $3, $4, $5)`,
			uuid.New(), tenantID, f.name, f.display, bucketID)
	}

	repo := adapters.NewCollectionRepo(sqlc.New(pool), pool)
	listArgs := func(filter string) objectkey.ListCollectionsArgs {
		return objectkey.ListCollectionsArgs{TenantID: tenantID, PageSize: 1000, Filter: filter}
	}
	all, _, err := repo.List(ctx, listArgs(""))
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	for _, q := range searchQueries {
		t.Run(q, func(t *testing.T) {
			filter := celFilter(q)
			narrowed, _, err := repo.List(ctx, listArgs(filter))
			if err != nil {
				t.Fatalf("list filtered: %v", err)
			}
			viaSQL := celCollections(t, filter, narrowed)
			viaAll := celCollections(t, filter, all)
			if len(viaSQL) != len(viaAll) {
				t.Errorf("pushdown changed the answer for %q: %d rows after SQL "+
					"narrowing, %d over every row — ListCollections' search_like "+
					"clause and cel.SearchText disagree", q, len(viaSQL), len(viaAll))
			}
		})
	}
}

func celCollections(t *testing.T, filter string, in []objectkey.Collection) []objectkey.Collection {
	t.Helper()
	if filter == "" {
		return in
	}
	in = append([]objectkey.Collection(nil), in...)
	out, err := celpkg.FilterPage(celpkg.NewEvaluator(), celpkg.CollectionSchema, filter, in,
		func(c objectkey.Collection) map[string]any {
			return map[string]any{
				"collection":   c.Collection,
				"display_name": c.DisplayName,
				"search":       celpkg.SearchText(c.Collection, c.DisplayName),
			}
		})
	if err != nil {
		t.Fatalf("cel filter %q: %v", filter, err)
	}
	return out
}

// The bug the field exists to end, for each list: a match that sorts past the
// page. Both had a TestPushdown_* case for `==` already; neither had one for a
// search, which is the shape the console actually sends.
func subtestBackendPastThePage(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {

	prefix := "page-" + uuid.NewString()[:8]
	for i := range 40 {
		mustExec(t, ctx, pool,
			`INSERT INTO storage_backends (name, kind, display_name)
			 VALUES ($1, 's3-compatible', '')`,
			fmt.Sprintf("%s-aaa-%03d", prefix, i))
	}
	mustExec(t, ctx, pool,
		`INSERT INTO storage_backends (name, kind, display_name)
		 VALUES ($1, 's3-compatible', 'The Needle')`, prefix+"-zzz")

	repo := adapters.NewBackendRepoV2(sqlc.New(pool), pool)
	got, _, err := repo.List(ctx, 5, "", `search.contains("needle")`)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].BackendID != prefix+"-zzz" {
		t.Errorf("first page of a filtered backend list = %d rows, want the one "+
			"named %s-zzz — the filter selected from the page instead of from "+
			"the table", len(got), prefix)
	}
}

// ONE Postgres for all five, not five.
//
// startPostgres runs a fresh container per call — about 55s each on this
// machine — and these started as five separate Test functions. The package
// went from 611s to 887s against a 1200s ceiling, which is the same ceiling
// that failed a verify-deep run earlier the same day when the local task was
// missing its -timeout. Adding tests should not be the thing that eventually
// breaks the gate.
//
// Sharing is safe because the fixtures do not collide: each subtest seeds
// under its own backend name or uuid-derived prefix, and each asserts only
// over rows it created.
func TestSearchPushdownContract(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)

	t.Run("tenants", func(t *testing.T) { subtestTenantSearch(t, ctx, pool) })
	t.Run("buckets", func(t *testing.T) { subtestBucketSearch(t, ctx, pool) })
	t.Run("backends", func(t *testing.T) { subtestBackendSearch(t, ctx, pool) })
	t.Run("collections", func(t *testing.T) { subtestCollectionSearch(t, ctx, pool) })
}

// The other half: a match that sorts past the page, which only a SQL predicate
// can reach.
func TestSearchFindsAMatchPastThePage(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)

	t.Run("buckets", func(t *testing.T) { subtestBucketPastThePage(t, ctx, pool) })
	t.Run("backends", func(t *testing.T) { subtestBackendPastThePage(t, ctx, pool) })
}

func subtestTenantSearch(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	repo := adapters.NewTenantRepo(sqlc.New(pool), pool)

	prefix := "srch" + uuid.NewString()[:8]
	for _, f := range searchFixtures {
		// Tenant slugs are constrained; the fixture names are slug-safe apart
		// from the separator probe pair, which is what it is for.
		slug := prefix + "-" + strings.ReplaceAll(f.name, "_", "-")
		// tenants_display_name_format requires 1..255 characters, so the
		// shared fixtures' empty-display-name row cannot exist here. That is a
		// real difference from buckets and backends, not a workaround: this
		// table has no such state to get wrong, and substituting a placeholder
		// keeps the row without pretending the empty case was covered.
		display := f.display
		if display == "" {
			display = "no display"
		}
		mustExec(t, ctx, pool,
			`INSERT INTO tenants (id, slug, display_name, storage_layout)
			 VALUES ($1, $2, $3, 'shared')`,
			uuid.New(), slug, display)
	}

	all, _, err := repo.List(ctx, tenanth.ListTenantsArgs{PageSize: 1000})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	for _, q := range searchQueries {
		t.Run(q, func(t *testing.T) {
			filter := celFilter(q)
			narrowed, _, err := repo.List(ctx, tenanth.ListTenantsArgs{PageSize: 1000, Filter: filter})
			if err != nil {
				t.Fatalf("list filtered: %v", err)
			}
			viaSQL := celTenants(t, filter, narrowed)
			viaAll := celTenants(t, filter, all)
			if len(viaSQL) != len(viaAll) {
				t.Errorf("pushdown changed the answer for %q: %d rows after SQL "+
					"narrowing, %d over every row — ListTenants' search_like clause "+
					"and cel.SearchText disagree", q, len(viaSQL), len(viaAll))
			}
		})
	}
}

func celTenants(t *testing.T, filter string, in []tenanth.Tenant) []tenanth.Tenant {
	t.Helper()
	if filter == "" {
		return in
	}
	in = append([]tenanth.Tenant(nil), in...)
	out, err := celpkg.FilterPage(celpkg.NewEvaluator(), celpkg.TenantSchema, filter, in,
		func(x tenanth.Tenant) map[string]any {
			return map[string]any{
				"slug":         x.Slug,
				"display_name": x.DisplayName,
				"search":       celpkg.SearchText(x.TenantID.String(), x.Slug, x.DisplayName),
			}
		})
	if err != nil {
		t.Fatalf("cel filter %q: %v", filter, err)
	}
	return out
}
