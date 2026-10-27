//go:build integration

package components

import (
	"context"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// An audit row is written in its actor's tenant whatever the request's own
// scope, through the row-level security the runtime connects under. Rows the
// handlers write themselves — a refresh-token replay, a tenant switch — carry
// no request principal, and were refused by audit_log's insert policy while
// the error went unread.
func TestAuditRowIsWrittenInItsActorsTenant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	pool := rlsPool(t, ctx, admin)
	repo := adapters.NewAuditRepoV2(sqlc.New(pool), pool)

	actor, other := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{actor, other} {
		mustExec(t, ctx, admin, `INSERT INTO tenants (id, slug, display_name) VALUES ($1, $2, $2)`,
			id, "t-"+id.String()[:8])
	}
	noHook := func(context.Context, pgx.Tx) error { return nil }

	for name, write := range map[string]func(admindomain.AuditEntry) error{
		"no principal": func(e admindomain.AuditEntry) error { return repo.Insert(ctx, e) },
		"acting in another tenant": func(e admindomain.AuditEntry) error {
			return repo.Insert(auth.WithActingTenant(ctx, other), e)
		},
		"with an outbox hook": func(e admindomain.AuditEntry) error {
			return repo.InsertWithOutbox(ctx, e, noHook)
		},
	} {
		t.Run(name, func(t *testing.T) {
			action := "test." + uuid.New().String()
			if err := write(admindomain.AuditEntry{
				ActorSubject: "user", ActorTenantID: actor, ActorAudience: auth.AudienceIAM,
				Action: action, ResourceName: "users/user",
			}); err != nil {
				t.Fatalf("insert refused: %v", err)
			}
			var n int
			if err := admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = $1 AND actor_tenant_id = $2`,
				action, actor).Scan(&n); err != nil || n != 1 {
				t.Errorf("rows = %d (%v), want 1", n, err)
			}
		})
	}
}
