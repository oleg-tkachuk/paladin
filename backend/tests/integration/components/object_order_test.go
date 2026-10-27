//go:build integration

package components

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/keyset"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// orderPageSize is small, so every walk crosses many pages and the keyset
// resumes between rows that share a sort value.
const orderPageSize = 4

// objectOrderOracle is each order_by as plain SQL orders it, written out here
// rather than read from the code under test.
var objectOrderOracle = map[string]string{
	"":                          "id",
	objecth.OrderByKey:          `path COLLATE "C"`,
	objecth.OrderByContentType:  `content_type COLLATE "C"`,
	objecth.OrderBySize:         "coalesce(size_bytes, 0)",
	objecth.OrderByState:        `state::text COLLATE "C"`,
	objecth.OrderByCreatedAt:    "created_at",
	objecth.OrderByTerminatedAt: "coalesce(terminated_at, 'epoch'::timestamptz)",
}

// Walking a listing sorted by any order_by, either way, pages by (value, id):
// every object once, in the order SQL sorts them, across live and trashed
// objects merged from two branches of the filter.
func TestListObjects_SortsByEveryOrderField(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	seedBucketRow(t, ctx, admin)
	seedCollectionFor(t, ctx, admin, tenant, "docs")
	// Values repeat on purpose; a third of the objects are trashed, at one of
	// two instants, and the rest have no terminated_at.
	mustExec(t, ctx, admin, `
		INSERT INTO objects (tenant_id, collection_id, path, state, content_type, size_bytes, created_at, terminated_at)
		SELECT $1, c.id,
		       (ARRAY['b', 'a', 'B', 'a/z', 'A'])[g % 5 + 1] || '-' || g,
		       CASE WHEN g % 3 = 0 THEN 'DELETED' ELSE 'AVAILABLE' END::object_state,
		       (ARRAY['image/png', 'application/pdf', 'text/plain'])[g % 3 + 1],
		       CASE WHEN g % 4 = 0 THEN NULL ELSE (g % 3) * 100 END,
		       '2026-10-01T00:00:00Z'::timestamptz + (g % 4) * interval '1 hour',
		       CASE WHEN g % 3 = 0 THEN '2026-10-02T00:00:00Z'::timestamptz + (g % 2) * interval '1 minute' END
		  FROM generate_series(1, 23) g
		  JOIN collections c ON c.tenant_id = $1 AND c.name = 'docs'`, tenant)

	pool := rlsPool(t, ctx, admin)
	repo := adapters.NewObjectRepo(sqlc.New(pool), pool)
	asTenant := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenant})
	const bothStates = `state == 'AVAILABLE' || state == 'DELETED'`
	prog, err := cel.NewEvaluator().Compile(cel.ObjectSchema, bothStates)
	if err != nil {
		t.Fatal(err)
	}

	fields := append([]string{""}, objecth.ObjectOrderFields...)
	if len(objectOrderOracle) != len(fields) {
		t.Fatalf("the oracle names %d orders, ListObjects accepts %d", len(objectOrderOracle), len(fields))
	}
	for _, field := range fields {
		for _, desc := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s desc=%v", field, desc), func(t *testing.T) {
				dir := "ASC"
				if desc {
					dir = "DESC"
				}
				want := idsOf(t, ctx, admin, fmt.Sprintf(
					`SELECT id FROM objects WHERE tenant_id = $1 ORDER BY %s %s, id %s`,
					objectOrderOracle[field], dir, dir), tenant)

				order := keyset.Order{Field: field, Desc: desc}
				var got []uuid.UUID
				token := ""
				for pages := 0; ; pages++ {
					if pages > len(want) {
						t.Fatal("walk did not terminate")
					}
					objs, next, err := repo.ListObjects(asTenant, objecth.ListObjectsArgs{
						TenantID: tenant, Collection: "docs", PageSize: orderPageSize,
						PageToken: token, CompiledCEL: prog, Filter: bothStates, Order: order,
					})
					if err != nil {
						t.Fatalf("page %d: %v", pages, err)
					}
					for _, o := range objs {
						got = append(got, o.ObjectID)
					}
					if next == "" {
						break
					}
					token = next
				}
				if !slices.Equal(got, want) {
					t.Fatalf("walked\n%v\nwant\n%v", got, want)
				}
			})
		}
	}

	// A page token pages only the order it was minted for.
	_, next, err := repo.ListObjects(asTenant, objecth.ListObjectsArgs{
		TenantID: tenant, Collection: "docs", PageSize: orderPageSize,
		CompiledCEL: prog, Filter: bothStates, Order: keyset.Order{Field: objecth.OrderByKey},
	})
	if err != nil || next == "" {
		t.Fatalf("first page: next %q, err %v", next, err)
	}
	_, _, err = repo.ListObjects(asTenant, objecth.ListObjectsArgs{
		TenantID: tenant, Collection: "docs", PageSize: orderPageSize, PageToken: next,
		CompiledCEL: prog, Filter: bothStates, Order: keyset.Order{Field: objecth.OrderByKey, Desc: true},
	})
	if !errors.Is(err, keyset.ErrOrderChanged) {
		t.Fatalf("token reused under another order: err = %v, want ErrOrderChanged", err)
	}
}

// An order the function does not know is refused, not listed in some order.
func TestSearchObjectIDs_RefusesAnUnknownOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	seedBucketRow(t, ctx, admin)
	seedCollectionFor(t, ctx, admin, tenant, "docs")
	// Every hint unset; only the order is under test.
	_, err := admin.Exec(ctx, `SELECT search_object_ids($1, 'docs', $2, $3, $4, $5, $6, $7, $8, $9, 10, false, $10, false, $11, NULL, NULL)`,
		tenant, nil, nil, nil, nil, nil, nil, nil, nil, "path; DROP TABLE objects", nil)
	if err == nil {
		t.Fatal("an unknown order listed objects")
	}
}

func idsOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, q string, args ...any) []uuid.UUID {
	t.Helper()
	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
