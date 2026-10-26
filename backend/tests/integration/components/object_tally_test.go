//go:build integration

package components

import (
	"context"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// TallyObjects counts what the listing would return, by state and content
// type: under row-level security, at the prefix and folder level, after the
// filter, and with trashed objects only when asked.
func TestTallyObjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	other, _ := mkTenant(t, ctx, admin, "shared")
	seedBucketRow(t, ctx, admin)
	seedCollectionFor(t, ctx, admin, tenant, "docs")
	seedCollectionFor(t, ctx, admin, other, "docs")
	insert := func(tenantArg any, path, state, contentType string) {
		mustExec(t, ctx, admin, `
			INSERT INTO objects (tenant_id, collection_id, path, state, content_type)
			SELECT $1, c.id, $2, $3::object_state, $4
			  FROM collections c WHERE c.tenant_id = $1 AND c.name = 'docs'`, tenantArg, path, state, contentType)
	}
	insert(tenant, "a/1.png", "AVAILABLE", "image/png")
	insert(tenant, "a/2.png", "PENDING", "image/png")
	insert(tenant, "a/3.pdf", "AVAILABLE", "application/pdf")
	insert(tenant, "a/deep/4.pdf", "AVAILABLE", "application/pdf")
	insert(tenant, "a/5.pdf", "DELETED", "application/pdf")
	insert(tenant, "b/6.png", "AVAILABLE", "image/png")
	insert(other, "a/7.png", "AVAILABLE", "image/png")

	pool := rlsPool(t, ctx, admin)
	repo := adapters.NewObjectRepo(sqlc.New(pool), pool)
	asTenant := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenant})
	ev := cel.NewEvaluator()
	key := func(s statemachine.State, ct string) objecth.ObjectTallyKey {
		return objecth.ObjectTallyKey{State: s, ContentType: ct}
	}

	for _, tc := range []struct {
		name string
		args objecth.TallyObjectsArgs
		want map[objecth.ObjectTallyKey]int64
	}{
		{"the folder level, live only", objecth.TallyObjectsArgs{Prefix: "a/", Delimiter: "/"}, map[objecth.ObjectTallyKey]int64{
			key(statemachine.StateAvailable, "image/png"):       1,
			key(statemachine.StatePending, "image/png"):         1,
			key(statemachine.StateAvailable, "application/pdf"): 1,
		}},
		{"every level under the prefix", objecth.TallyObjectsArgs{Prefix: "a/"}, map[objecth.ObjectTallyKey]int64{
			key(statemachine.StateAvailable, "image/png"):       1,
			key(statemachine.StatePending, "image/png"):         1,
			key(statemachine.StateAvailable, "application/pdf"): 2,
		}},
		{"trashed when asked", objecth.TallyObjectsArgs{Prefix: "a/", Delimiter: "/", IncludeDeleted: true}, map[objecth.ObjectTallyKey]int64{
			key(statemachine.StateAvailable, "image/png"):       1,
			key(statemachine.StatePending, "image/png"):         1,
			key(statemachine.StateAvailable, "application/pdf"): 1,
			key(statemachine.StateDeleted, "application/pdf"):   1,
		}},
		{"after the filter", objecth.TallyObjectsArgs{Filter: `key.endsWith(".png")`}, map[objecth.ObjectTallyKey]int64{
			key(statemachine.StateAvailable, "image/png"): 2,
			key(statemachine.StatePending, "image/png"):   1,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := tc.args
			args.TenantID, args.Collection = tenant, "docs"
			if args.Filter != "" {
				args.CompiledCEL = must(ev.Compile(cel.ObjectSchema, args.Filter))
			}
			got, err := repo.TallyObjects(asTenant, args)
			if err != nil {
				t.Fatalf("TallyObjects: %v", err)
			}
			if !got.Exact {
				t.Error("a small listing tallied inexactly")
			}
			if len(got.Counts) != len(tc.want) {
				t.Errorf("tally = %v, want %v", got.Counts, tc.want)
			}
			for k, n := range tc.want {
				if got.Counts[k] != n {
					t.Errorf("%v = %d, want %d", k, got.Counts[k], n)
				}
			}
		})
	}
}
