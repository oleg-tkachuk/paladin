//go:build integration

package components

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// previewNames is a limit below what the tenant holds, so the preview must
// name some and count all.
const previewNames = 2

// seedTokens is how many API tokens the tenant holds, more than previewNames.
const seedTokens = 3

// The preview is read on the application role, where row-level security hides
// every tenant's tokens from a caller not acting on that tenant: a preview
// read without that would answer "nothing to remove" for a tenant holding
// plenty. It names the first tokens and subscriptions, counts them all and
// the tenant's capabilities, and counts what refuses the purge — and another
// tenant's rows are none of it.
func TestThePurgePreviewReadsTheTenantsRowsUnderRLS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	pool := rlsPool(t, ctx, admin)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	other, _ := mkTenant(t, ctx, admin, "shared")

	for _, owner := range []uuid.UUID{tenant, other} {
		for i := range seedTokens {
			mustExec(t, ctx, admin, `INSERT INTO api_tokens
				(id, tenant_id, name, prefix, token_hmac, scopes, audience, expires_at, created_by)
				VALUES ($1, $2, $3, 'AAAAAAAA', $4, '{}'::text[], '{data}'::text[], now() + interval '1 year', 'test')`,
				uuid.New(), owner, fmt.Sprintf("token-%d", i), []byte(uuid.NewString()))
		}
		mustExec(t, ctx, admin, `INSERT INTO event_subscriptions (id, tenant_id, sink_kind, sink_config)
			VALUES ($1, $2, 'http', '{}'::jsonb)`, uuid.New(), owner)
		mustExec(t, ctx, admin,
			`INSERT INTO users (tenant_id, subject, password_hash, roles) VALUES ($1, 'holder', 'x', '{}')`, owner)
	}
	records := newCapStore(t, pool)
	for _, subject := range []string{"agent:a", "agent:b"} {
		c := mkCap(tenant, subject, time.Now().Add(time.Hour))
		if err := records.Insert(auth.WithActingTenant(ctx, tenant), limes.InsertRequest{Capability: c, IssuedBy: seedIssuer}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := adapters.NewTenantRepo(sqlc.New(pool), pool).PreviewPurge(ctx, tenant, previewNames)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.APITokens) != previewNames || got.APITokenCount != seedTokens {
		t.Errorf("api tokens: named %d of %d, want %d of %d", len(got.APITokens), got.APITokenCount, previewNames, seedTokens)
	}
	if got.APITokens[0].Name != "token-0" {
		t.Errorf("first token %q, want the first issued, token-0", got.APITokens[0].Name)
	}
	if len(got.Subscriptions) != 1 || got.SubscriptionCount != 1 || got.Subscriptions[0].SinkKind != "http" {
		t.Errorf("subscriptions: %+v of %d, want one http", got.Subscriptions, got.SubscriptionCount)
	}
	if got.CapabilityCount != 2 {
		t.Errorf("capabilities: %d, want 2", got.CapabilityCount)
	}
	if want := (tenanth.PurgeBlockers{Users: 1}); got.Blockers != want {
		t.Errorf("blockers: %+v, want %+v", got.Blockers, want)
	}
}
