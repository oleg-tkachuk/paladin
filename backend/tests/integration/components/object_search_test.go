//go:build integration

// Object search under row-level security (migrations 025–029).
//
// The runtime role cannot use the trigram and GIN indexes directly: `@>` and
// LIKE are not LEAKPROOF, so RLS keeps them out of the plan. ListObjects
// therefore gets its candidate ids from search_object_ids, a SECURITY DEFINER
// function, and reads the rows themselves under RLS. These tests pin the two
// halves of that bargain: the function really is fast (its queries use the
// indexes), and it really is scoped (it answers nothing about a tenant the
// session is not acting on).
package components

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// searchIDs calls search_object_ids with only a tags predicate.
func searchIDs(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant uuid.UUID, collection, tags string) ([]uuid.UUID, error) {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT search_object_ids($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, 1000)`,
		tenant, collection, nil, nil, nil, nil, nil, tags, nil, nil)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func TestSearchObjectIDs_ScopedToTheSessionTenant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)

	tenantA, _ := mkTenant(t, ctx, admin, "shared")
	tenantB, _ := mkTenant(t, ctx, admin, "shared")
	seedBucketRow(t, ctx, admin)
	seedCollectionFor(t, ctx, admin, tenantA, "docs")
	seedCollectionFor(t, ctx, admin, tenantB, "docs")
	for _, tid := range []uuid.UUID{tenantA, tenantB} {
		mustExec(t, ctx, admin, `
			INSERT INTO objects (tenant_id, collection_id, path, state, content_type, tags)
			SELECT $1, c.id, 'k-' || g, 'AVAILABLE', 'text/plain', '{"env":"prod"}'
			  FROM generate_series(1, 5) g
			  JOIN collections c ON c.tenant_id = $1 AND c.name = 'docs'`, tid)
	}
	pool := rlsPool(t, ctx, admin)
	asA := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenantA})

	got, err := searchIDs(t, asA, pool, tenantA, "docs", `{"env":"prod"}`)
	if err != nil || len(got) != 5 {
		t.Fatalf("own tenant: %d ids, err %v; want 5", len(got), err)
	}
	// Asking about another tenant's keys returns nothing — not an error, so
	// it is indistinguishable from "no match".
	if got, err := searchIDs(t, asA, pool, tenantB, "docs", `{"env":"prod"}`); err != nil || len(got) != 0 {
		t.Fatalf("other tenant: %d ids, err %v; want none", len(got), err)
	}
	// No tenant in the session: nothing, like the policy.
	if got, err := searchIDs(t, ctx, pool, tenantA, "docs", `{"env":"prod"}`); err != nil || len(got) != 0 {
		t.Fatalf("no session tenant: %d ids, err %v; want none", len(got), err)
	}
	// The admin plane's cross-tenant read flag widens it exactly as it
	// widens the policy's USING.
	cross := auth.WithCrossTenantRead(asA)
	if got, err := searchIDs(t, cross, pool, tenantB, "docs", `{"env":"prod"}`); err != nil || len(got) != 5 {
		t.Fatalf("cross-tenant read: %d ids, err %v; want 5", len(got), err)
	}
}

// A SECURITY DEFINER function is callable by PUBLIC unless revoked; 029
// revokes it. A role with table grants but no EXECUTE must be refused.
func TestSearchObjectIDs_NotExecutableByPublic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	var db string
	if err := admin.QueryRow(ctx, `SELECT current_database()`).Scan(&db); err != nil {
		t.Fatal(err)
	}
	role := "search_public_" + db
	mustExec(t, ctx, admin, `DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '`+role+`') THEN
			CREATE ROLE "`+role+`" LOGIN PASSWORD 'p' NOBYPASSRLS;
		END IF;
	END $$`)
	mustExec(t, ctx, admin, `GRANT USAGE ON SCHEMA public TO "`+role+`"`)
	cfg := admin.Config().Copy()
	cfg.ConnConfig.User = role
	cfg.ConnConfig.Password = "p"
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	_, err = searchIDs(t, ctx, p, uuid.New(), "docs", `{}`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("err = %v, want insufficient_privilege (42501)", err)
	}
}

// ListObjects through the RLS pool: the pushdown narrows in SQL and the CEL
// pass still decides. A filter that matches one object in a thousand comes
// back on the first page instead of as an empty page with a cursor.
func TestListObjects_SearchUnderRLS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenantA, _ := mkTenant(t, ctx, admin, "shared")
	tenantB, _ := mkTenant(t, ctx, admin, "shared")
	seedBucketRow(t, ctx, admin)
	for _, tid := range []uuid.UUID{tenantA, tenantB} {
		seedCollectionFor(t, ctx, admin, tid, "docs")
		mustExec(t, ctx, admin, `
			INSERT INTO objects (tenant_id, collection_id, path, state, content_type, tags)
			SELECT $1, c.id, 'reports/q_' || g || '.pdf', 'AVAILABLE',
			       CASE WHEN g % 2 = 0 THEN 'application/pdf' ELSE 'image/png' END,
			       jsonb_build_object('env', CASE WHEN g % 1000 = 0 THEN 'prod' ELSE 'dev' END)
			  FROM generate_series(1, 3000) g
			  JOIN collections c ON c.tenant_id = $1 AND c.name = 'docs'`, tid)
	}
	pool := rlsPool(t, ctx, admin)
	repo := adapters.NewObjectRepo(sqlc.New(pool), pool)
	asA := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenantA})
	ev := cel.NewEvaluator()

	list := func(filter string) ([]objecth.Object, string) {
		t.Helper()
		prog, err := ev.Compile(cel.ObjectSchema, filter)
		if err != nil {
			t.Fatal(err)
		}
		objs, next, err := repo.ListObjects(asA, objecth.ListObjectsArgs{
			TenantID: tenantA, Collection: "docs", PageSize: 100, CompiledCEL: prog, Filter: filter,
		})
		if err != nil {
			t.Fatalf("ListObjects(%s): %v", filter, err)
		}
		return objs, next
	}

	// Rows without the tag key are dropped in SQL, so the CEL pass never
	// meets them — before the pushdown this filter failed with "no such key".
	objs, next := list(`tags['env'] == 'prod'`)
	if len(objs) != 3 || next != "" {
		t.Fatalf("tags: %d objects, next %q; want 3 and no further page", len(objs), next)
	}
	// An underscore is a LIKE wildcard; escaped, it only matches itself.
	if objs, _ := list(`key.contains('q_123')`); len(objs) != 11 {
		t.Fatalf("key.contains(q_123): %d objects, want 11 (q_123, q_1230..q_1239)", len(objs))
	}
	// Of those, the even g are PDFs: q_1230, 1232, 1234, 1236, 1238.
	if objs, _ := list(`key.contains('q_123') && content_type == 'application/pdf'`); len(objs) != 5 {
		t.Fatalf("key + content_type: %d objects, want 5", len(objs))
	}
	for _, o := range objs {
		if o.TenantID != tenantA {
			t.Fatalf("object of tenant %s in tenant %s's listing", o.TenantID, tenantA)
		}
	}
	n, exact, err := repo.CountObjects(asA, objecth.CountObjectsArgs{
		TenantID: tenantA, Collection: "docs",
		CompiledCEL: must(ev.Compile(cel.ObjectSchema, `content_type.startsWith('image/')`)),
		Filter:      `content_type.startsWith('image/')`,
	})
	if err != nil || n != 1500 || !exact {
		t.Fatalf("count: %d exact=%v err=%v; want 1500 exact", n, exact, err)
	}
}

// A filter with `||` or `in` runs one indexed query per branch and merges
// them. Walking every page with a small page size has to return exactly the
// objects the filter accepts — once each, none skipped where one branch fills
// its page and another reaches further.
func TestListObjects_DisjunctionUnderRLS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	seedBucketRow(t, ctx, admin)
	seedCollectionFor(t, ctx, admin, tenant, "docs")
	mustExec(t, ctx, admin, `
		INSERT INTO objects (tenant_id, collection_id, path, state, content_type, tags)
		SELECT $1, c.id, 'k/' || g, 'AVAILABLE',
		       CASE WHEN g % 7 = 0 THEN 'image/png' ELSE 'text/plain' END,
		       CASE WHEN g % 50 = 0 THEN '{"env":"prod"}'::jsonb
		            WHEN g % 11 = 0 THEN '{"env":"stage"}'::jsonb
		            ELSE '{}'::jsonb END
		  FROM generate_series(1, 2000) g
		  JOIN collections c ON c.tenant_id = $1 AND c.name = 'docs'`, tenant)
	pool := rlsPool(t, ctx, admin)
	repo := adapters.NewObjectRepo(sqlc.New(pool), pool)
	asT := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenant})
	ev := cel.NewEvaluator()

	walk := func(filter string) map[string]bool {
		t.Helper()
		prog := must(ev.Compile(cel.ObjectSchema, filter))
		seen := map[string]bool{}
		token := ""
		for pages := 0; ; pages++ {
			if pages > 2000 {
				t.Fatalf("%s: paging did not terminate", filter)
			}
			objs, next, err := repo.ListObjects(asT, objecth.ListObjectsArgs{
				TenantID: tenant, Collection: "docs", PageSize: 7, PageToken: token,
				CompiledCEL: prog, Filter: filter,
			})
			if err != nil {
				t.Fatalf("%s: %v", filter, err)
			}
			for _, o := range objs {
				if seen[o.Key] {
					t.Fatalf("%s: %s returned twice", filter, o.Key)
				}
				seen[o.Key] = true
			}
			if next == "" {
				return seen
			}
			token = next
		}
	}

	// g%50 → 40 prod; g%7 → 285 png; both → g%350 → 5. Union 320.
	if got := walk(`tags['env'] == 'prod' || content_type == 'image/png'`); len(got) != 320 {
		t.Errorf("prod || png: %d objects, want 320", len(got))
	}
	// prod 40 + stage: g%11 and not g%50 → 181 - 3 (g%550) = 178. 218 total.
	if got := walk(`tags['env'] in ['prod', 'stage']`); len(got) != 218 {
		t.Errorf("env in [prod, stage]: %d objects, want 218", len(got))
	}
	n, exact, err := repo.CountObjects(asT, objecth.CountObjectsArgs{
		TenantID: tenant, Collection: "docs",
		CompiledCEL: must(ev.Compile(cel.ObjectSchema, `tags['env'] == 'prod' || content_type == 'image/png'`)),
		Filter:      `tags['env'] == 'prod' || content_type == 'image/png'`,
	})
	if err != nil || n != 320 || !exact {
		t.Errorf("count prod || png: %d exact=%v err=%v; want 320 exact", n, exact, err)
	}
}

// The function's queries, as its owner runs them, use the indexes 026–028.
// EXPLAIN cannot see inside a plpgsql call, so this explains the statements
// it builds — keep them in step with 029.
func TestIndexUsage_ObjectSearch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	mustExec(t, ctx, pool, `
		INSERT INTO objects (tenant_id, collection_id, path, state, content_type, tags, metadata)
		SELECT $1, $2, 'logs/report_' || g || '.pdf', 'AVAILABLE', 'application/pdf',
		       jsonb_build_object('env', CASE WHEN g % 1000 = 0 THEN 'prod' ELSE 'dev' END),
		       jsonb_build_object('owner', 'team' || (g % 500))
		  FROM generate_series(1, 20000) g`, f.tenantID, f.collectionID)
	analyze(t, ctx, pool, "objects")

	const base = `SELECT o.id FROM public.objects o
		WHERE o.tenant_id = $1 AND o.collection_id = $2`
	for _, tc := range []struct {
		what, pred, index string
		arg               any
	}{
		{"tag equality", ` AND o.tags @> $3`, "idx_objects_tags_gin", `{"env":"prod"}`},
		{"metadata equality", ` AND o.metadata @> $3`, "idx_objects_metadata_gin", `{"owner":"team7"}`},
		{"key substring", ` AND o.path LIKE '%' || $3 || '%'`, "idx_objects_path_trgm", `report\_1234`},
	} {
		plan := explain(t, ctx, pool, base+tc.pred+` ORDER BY o.id LIMIT 100`, f.tenantID, f.collectionID, tc.arg)
		assertPlanUses(t, plan, tc.index, tc.what)
		assertPlanAvoidsSeqScan(t, plan, "objects", tc.what)
	}
}

// A status-filtered page reads state from the keyset index (030), without
// touching the heap. Seeded across many Collections for the reason
// TestIndexUsage_ObjectsKeysetPagination gives: with the whole tenant in one
// Collection the tenant's own (tenant_id, id) index is just as good, and the
// test would prove nothing.
func TestIndexUsage_ObjectsStatePage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	keys := seedCollections(t, ctx, pool, f, 20)
	for _, k := range keys {
		seedObjectsUnder(t, ctx, pool, f, k, 500, "AVAILABLE")
	}
	mustExec(t, ctx, pool, "VACUUM ANALYZE objects")

	var probe uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM collections WHERE tenant_id = $1 AND name = $2`,
		f.tenantID, keys[0]).Scan(&probe); err != nil {
		t.Fatalf("resolve probe collection: %v", err)
	}
	plan := explain(t, ctx, pool, `SELECT o.id FROM public.objects o
		WHERE o.tenant_id = $1 AND o.collection_id = $2 AND o.state = $3::object_state
		ORDER BY o.id LIMIT 100`, f.tenantID, probe, "AVAILABLE")
	assertPlanUses(t, plan, "Index Only Scan using idx_objects_keyset_state", "status-filtered page")
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
