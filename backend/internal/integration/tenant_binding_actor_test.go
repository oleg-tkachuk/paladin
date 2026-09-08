//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// tenant_default_bindings.set_by records who pointed a tenant at its default
// bucket. The handler that sets one later resolves the principal explicitly,
// with a comment saying that recording "" would "leave a binding nobody can
// be held to" — but the binding written at tenant-creation time took its
// actor from actorFromContext, which read a context key nothing writes and
// asserted to a method *Principal does not have. It returned "" for every
// caller, and "" is also its legitimate answer on the bootstrap paths, so an
// empty column never looked wrong.
//
// The unit test pins the extraction; this pins the column, which is the thing
// an operator reads.
func TestTenantCreate_RecordsBindingActor(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)

	const backendID = "be-actor"
	mustExec(t, ctx, pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, backendID)

	const bucketName = "bucket-actor"
	mustExec(t, ctx, pool,
		`INSERT INTO buckets (backend_id, name) SELECT sb.id, $2 FROM storage_backends sb WHERE sb.name = $1`,
		backendID, bucketName)

	repo := adapters.NewTenantRepo(sqlc.New(pool), pool)

	cases := map[string]struct {
		ctx  context.Context
		want string
	}{
		// The ordinary path: an authenticated operator creates a tenant.
		"authenticated caller": {
			ctx:  auth.WithPrincipal(ctx, &auth.Principal{Subject: "admin@local"}),
			want: "admin@local",
		},
		// Bootstrap and seed scripts run with no principal. Empty is the
		// right answer there — the column is NOT NULL — and keeping this
		// case makes the difference between the two the assertion.
		"no principal": {ctx: ctx, want: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tid := uuid.New()
			slug := "actor-" + uuid.NewString()[:8]
			if _, err := repo.Create(tc.ctx, tenant.CreateTenantArgs{
				TenantID:          tid,
				Slug:              slug,
				DisplayName:       slug,
				DefaultBackendID:  backendID,
				DefaultBucketName: bucketName,
			}); err != nil {
				t.Fatalf("create tenant: %v", err)
			}

			var setBy string
			if err := pool.QueryRow(ctx,
				`SELECT set_by FROM tenant_default_bindings WHERE tenant_id = $1`, tid,
			).Scan(&setBy); err != nil {
				t.Fatalf("read set_by: %v — the binding was not written", err)
			}
			if setBy != tc.want {
				t.Errorf("set_by = %q, want %q", setBy, tc.want)
			}
		})
	}
}
