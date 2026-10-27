//go:build integration

package components

import (
	"context"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// The storage-migration RPCs are a platform admin's, and a platform admin's
// principal is bound to its own tenant, not the one it migrates. Through the
// runtime's RLS pool every query is scoped to the tenant the request acts
// on, so the handler has to act as the tenant it authorised: otherwise the
// target's collections and migration row are filtered away (no source
// bucket, no migration) and the start's INSERT trips WITH CHECK. The other
// storage-migration tests use the suite's superuser pool, where RLS never
// engages.
func TestStorageMigrationRPCActsOnTheNamedTenant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)

	pool := rlsPool(t, ctx, f.pool)
	h := tenanth.NewHandler(adapters.NewTenantRepo(sqlc.New(pool), pool), allowAll{})
	ctx = auth.WithPrincipal(ctx, &auth.Principal{
		Subject: "operator", TenantID: uuid.New(), Roles: []string{apiutil.RolePlatformAdmin},
	})

	m, err := h.MigrateTenantStorageLayout(ctx, f.tenant, "", 0)
	if err != nil {
		t.Fatalf("migrate another tenant: %v", err)
	}
	if m.State != worker.MigStateProvisioning || m.SourceBucketName != f.private {
		t.Errorf("migration = %s from %s, want provisioning from %s", m.State, m.SourceBucketName, f.private)
	}

	got, err := h.GetTenantStorageMigration(ctx, f.tenant)
	if err != nil {
		t.Fatalf("read another tenant's migration: %v", err)
	}
	if got.TenantID != f.tenant || got.State != m.State || got.TargetBucketName != m.TargetBucketName {
		t.Errorf("read migration %+v, want the one just started (%+v)", got, m)
	}
}

// The same for a tenant left dedicated by a verify failure before rollbacks
// existed: the RPC has to find its failed migration to hand it to the worker.
func TestStorageMigrationRPCRollsBackAnotherTenant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	f.stuckAfterVerify(t, ctx)

	pool := rlsPool(t, ctx, f.pool)
	h := tenanth.NewHandler(adapters.NewTenantRepo(sqlc.New(pool), pool), allowAll{})
	ctx = auth.WithPrincipal(ctx, &auth.Principal{
		Subject: "operator", TenantID: uuid.New(), Roles: []string{apiutil.RolePlatformAdmin},
	})

	m, err := h.MigrateTenantStorageLayout(ctx, f.tenant, "", 0)
	if err != nil {
		t.Fatalf("roll back another tenant: %v", err)
	}
	if m.State != worker.MigStateRollingBack {
		t.Errorf("migration = %s, want rolling_back", m.State)
	}
}
