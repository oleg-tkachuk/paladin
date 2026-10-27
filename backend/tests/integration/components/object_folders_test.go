//go:build integration

// A folder listing (089): one level of a collection under a prefix, cut in
// SQL, and the folders below it.
package components

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

const folderDelimiter = "/"

func TestListObjects_OneFolderLevel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	seedBucketRow(t, ctx, admin)
	seedCollectionFor(t, ctx, admin, tenant, "docs")
	put := func(path, state string) {
		t.Helper()
		mustExec(t, ctx, admin, `
			INSERT INTO objects (tenant_id, collection_id, path, state, content_type)
			SELECT $1, c.id, $2, $3::object_state, 'text/plain'
			  FROM collections c WHERE c.tenant_id = $1 AND c.name = 'docs'`, tenant, path, state)
	}
	for _, p := range []string{
		"top.txt",
		"a/one.txt", "a/two.txt", "a/b/deep.txt", "a/b/c/deeper.txt",
		// An underscore and a percent are LIKE wildcards: the folder a_%/
		// must not take in aXY/.
		"a_%/x.txt", "aXY/y.txt",
	} {
		put(p, "AVAILABLE")
	}
	// A folder whose only object is trashed is no folder of the live list.
	put("a/gone/old.txt", "DELETED")
	// Many objects one level down must not come back as empty pages.
	for i := range 250 {
		put(fmt.Sprintf("a/many/%03d.txt", i), "AVAILABLE")
	}

	pool := rlsPool(t, ctx, admin)
	repo := adapters.NewObjectRepo(sqlc.New(pool), pool)
	as := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenant})
	ev := cel.NewEvaluator()

	keys := func(prefix, filter string, pageSize int32) []string {
		t.Helper()
		var out []string
		token := ""
		for {
			objs, next, err := repo.ListObjects(as, objecth.ListObjectsArgs{
				TenantID: tenant, Collection: "docs", PageSize: pageSize, PageToken: token,
				CompiledCEL: must(ev.Compile(cel.ObjectSchema, filter)), Filter: filter,
				Prefix: prefix, Delimiter: folderDelimiter,
			})
			if err != nil {
				t.Fatalf("ListObjects(%q): %v", prefix, err)
			}
			if len(objs) == 0 && next != "" {
				t.Fatalf("ListObjects(%q): an empty page with a cursor", prefix)
			}
			for _, o := range objs {
				out = append(out, o.Key)
			}
			if next == "" {
				break
			}
			token = next
		}
		slices.Sort(out)
		return out
	}
	folders := func(prefix, filter string) []string {
		t.Helper()
		got, truncated, err := repo.ListCommonPrefixes(as, objecth.ListPrefixesArgs{
			TenantID: tenant, Collection: "docs", Prefix: prefix, Delimiter: folderDelimiter, Filter: filter,
		})
		if err != nil || truncated {
			t.Fatalf("ListCommonPrefixes(%q) truncated=%v err=%v", prefix, truncated, err)
		}
		return got
	}

	if got := keys("", "", 100); !slices.Equal(got, []string{"top.txt"}) {
		t.Errorf("root objects = %v", got)
	}
	if got := folders("", ""); !slices.Equal(got, []string{"a/", "aXY/", "a_%/"}) {
		t.Errorf("root folders = %v", got)
	}
	if got := keys("a/", "", 100); !slices.Equal(got, []string{"a/one.txt", "a/two.txt"}) {
		t.Errorf("a/ objects = %v", got)
	}
	if got := folders("a/", ""); !slices.Equal(got, []string{"a/b/", "a/many/"}) {
		t.Errorf("a/ folders = %v, want the live ones", got)
	}
	// Asking for the trash shows the folder that holds only trashed objects.
	if got := folders("a/", `state == "DELETED"`); !slices.Equal(got, []string{"a/gone/"}) {
		t.Errorf("a/ trashed folders = %v", got)
	}
	if got := keys("a_%/", "", 100); !slices.Equal(got, []string{"a_%/x.txt"}) {
		t.Errorf("a_%%/ objects = %v, the wildcards matched more", got)
	}
	// Paged in tens: every page carries objects, and all 250 come back.
	if got := keys("a/many/", "", 10); len(got) != 250 {
		t.Errorf("a/many/ = %d objects, want 250", len(got))
	}
	// The filter's other terms still decide over the objects.
	if got := keys("a/", `key.endsWith("two.txt")`, 100); !slices.Equal(got, []string{"a/two.txt"}) {
		t.Errorf("filtered a/ objects = %v", got)
	}
}

func TestListCommonPrefixes_Truncated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	seedBucketRow(t, ctx, admin)
	seedCollectionFor(t, ctx, admin, tenant, "docs")
	mustExec(t, ctx, admin, `
		INSERT INTO objects (tenant_id, collection_id, path, state, content_type)
		SELECT $1, c.id, 'f' || lpad(g::text, 5, '0') || '/x', 'AVAILABLE', 'text/plain'
		  FROM generate_series(1, $2::int) g
		  JOIN collections c ON c.tenant_id = $1 AND c.name = 'docs'`, tenant, objecth.MaxCommonPrefixes+1)
	pool := rlsPool(t, ctx, admin)
	repo := adapters.NewObjectRepo(sqlc.New(pool), pool)
	as := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenant})

	got, truncated, err := repo.ListCommonPrefixes(as, objecth.ListPrefixesArgs{
		TenantID: tenant, Collection: "docs", Delimiter: folderDelimiter,
	})
	if err != nil || !truncated || len(got) != objecth.MaxCommonPrefixes {
		t.Fatalf("got %d folders truncated=%v err=%v; want %d and truncated", len(got), truncated, err, objecth.MaxCommonPrefixes)
	}
	if got[0] != "f00001/" {
		t.Errorf("first folder = %q, want them in byte order", got[0])
	}
}

// The folders function is SECURITY DEFINER like search_object_ids, so it
// answers only the session's tenant, and PUBLIC may not call it.
func TestListObjectPrefixes_Scoped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenantA, _ := mkTenant(t, ctx, admin, "shared")
	tenantB, _ := mkTenant(t, ctx, admin, "shared")
	seedBucketRow(t, ctx, admin)
	for _, tid := range []uuid.UUID{tenantA, tenantB} {
		seedCollectionFor(t, ctx, admin, tid, "docs")
		mustExec(t, ctx, admin, `
			INSERT INTO objects (tenant_id, collection_id, path, state, content_type)
			SELECT $1, c.id, 'x/1', 'AVAILABLE', 'text/plain'
			  FROM collections c WHERE c.tenant_id = $1 AND c.name = 'docs'`, tid)
	}
	pool := rlsPool(t, ctx, admin)
	asA := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenantA})

	if got, err := prefixesOf(asA, pool, tenantA); err != nil || !slices.Equal(got, []string{"x/"}) {
		t.Fatalf("own tenant: %v, err %v", got, err)
	}
	if got, err := prefixesOf(asA, pool, tenantB); err != nil || len(got) != 0 {
		t.Fatalf("other tenant: %v, err %v; want none", got, err)
	}
	if got, err := prefixesOf(ctx, pool, tenantA); err != nil || len(got) != 0 {
		t.Fatalf("no session tenant: %v, err %v; want none", got, err)
	}
	if got, err := prefixesOf(auth.WithCrossTenantRead(asA), pool, tenantB); err != nil || len(got) != 1 {
		t.Fatalf("cross-tenant read: %v, err %v; want one", got, err)
	}

	var db string
	if err := admin.QueryRow(ctx, `SELECT current_database()`).Scan(&db); err != nil {
		t.Fatal(err)
	}
	role := "prefixes_public_" + db
	mustExec(t, ctx, admin, `DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '`+role+`') THEN
			CREATE ROLE "`+role+`" LOGIN PASSWORD 'p' NOBYPASSRLS;
		END IF;
	END $$`)
	mustExec(t, ctx, admin, `GRANT USAGE ON SCHEMA public TO "`+role+`"`)
	cfg := admin.Config().Copy()
	cfg.ConnConfig.User = role
	cfg.ConnConfig.Password = "p"
	public, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer public.Close()
	_, err = prefixesOf(ctx, public, tenantA)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != insufficientPrivilege {
		t.Fatalf("PUBLIC: err = %v, want insufficient_privilege", err)
	}
}

// insufficientPrivilege is Postgres' SQLSTATE for a call without EXECUTE.
const insufficientPrivilege = "42501"

func prefixesOf(ctx context.Context, p *pgxpool.Pool, tenant uuid.UUID) ([]string, error) {
	rows, err := p.Query(ctx, `SELECT list_object_prefixes($1, 'docs', '', '/', NULL, true, 10)`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
