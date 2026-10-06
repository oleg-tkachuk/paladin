//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/policyh"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// allowInspect admits the inspection; the gate is not what this test is about.
type allowInspect struct{}

func (allowInspect) IsAuthorized(context.Context, *cedar.Principal, cedar.Action, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionAllow, nil
}

// A platform admin inspecting another tenant's collection sees that
// collection's policy and its bucket's. The layers were read in the admin's
// own scope, where RLS hides the other tenant's collection, so both came back
// empty — and the page showed a collection as though it had no rules.
func TestEffectivePolicyOfAnotherTenantsCollection(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	platform := mustCreateTenant(t, h.PoolMigrate, "effective-platform")
	target := mustCreateTenant(t, h.PoolMigrate, "effective-target")
	const collection = "e2e/logs"
	mustCreateCollection(t, h.PoolMigrate, target, collection)
	const collectionRule = `forbid(principal, action == Action::"DeleteObject", resource);`
	if _, err := h.PoolMigrate.Exec(ctx,
		`UPDATE collections SET cedar_policy = $3 WHERE tenant_id = $1 AND name = $2`,
		target, collection, collectionRule); err != nil {
		t.Fatalf("set collection policy: %v", err)
	}

	cfg := wiringConfig(t, h.MigrateDSN)
	cfg.Datastores.Postgres.DSN = h.AppDSN // the runtime role, NOBYPASSRLS
	db, err := postgres.New(ctx, cfg.Datastores.Postgres, zap.NewNop(), postgres.WithRLS())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(db.Close)
	pool, ok := db.Pool.(*pgxpool.Pool)
	if !ok {
		t.Fatalf("db.Pool is %T, want *pgxpool.Pool", db.Pool)
	}
	handler := policyh.NewHandler(allowInspect{}, cedar.NewPostgresStore(pool))

	admin := auth.WithPrincipal(ctx, &auth.Principal{
		Subject: "ops", TenantID: platform, Roles: []string{"platform.admin"},
	})
	out, err := handler.GetEffectivePolicy(admin, "tenants/"+target.String()+"/collections/"+collection, platform)
	if err != nil {
		t.Fatalf("GetEffectivePolicy: %v", err)
	}
	byName := map[string]policyh.PolicyLayer{}
	for _, l := range out.Layers {
		byName[l.Source] = l
	}
	if l := byName["tenants/"+target.String()+"/collections/"+collection]; l.CedarPolicy != collectionRule {
		t.Errorf("collection layer = %q, want its policy", l.CedarPolicy)
	}
	if _, ok := byName["storageBackends/primary/buckets/paladin-test"]; !ok {
		t.Errorf("no bucket layer among %v", keys(byName))
	}
}

func keys(m map[string]policyh.PolicyLayer) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
