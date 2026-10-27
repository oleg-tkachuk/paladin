//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// StartStorageMigration as production runs it: as paladin_app, with the
// connection scoped to the tenant being migrated. The start must still see
// another tenant's use of the bucket it is about to take — RLS hides exactly
// those rows from a tenant-scoped connection — and must still be able to lock
// the tenant and record (and retry) the migration.
func TestStorageMigrationStartUnderRLS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := pgharness.Setup(t)
	const backend = "be-migration-start"
	seedBackend(t, h.PoolMigrate, backend)

	seed := func(t *testing.T, slug string) (uuid.UUID, tenanth.StartStorageMigrationArgs) {
		t.Helper()
		tenant := mustCreateTenant(t, h.PoolMigrate, slug)
		shared := "shared-" + slug
		mustSeedSharedBucket(t, h.PoolMigrate, backend, shared, tenant)
		exec(t, h, `INSERT INTO collections (tenant_id, name, bucket_id)
			SELECT $1, 'docs', b.id FROM buckets b WHERE b.name = $2`, tenant, shared)
		return tenant, tenanth.StartStorageMigrationArgs{
			TenantID:          tenant,
			SourceBackendName: backend, SourceBucketName: shared,
			TargetBackendName: backend, TargetBucketName: "paladin-" + tenant.String(),
			CleanupRetentionSeconds: 60,
		}
	}
	repo := adapters.NewTenantRepo(sqlc.New(h.PoolApp), h.PoolApp)
	as := func(tenant uuid.UUID) context.Context {
		return auth.WithPrincipal(ctx, &auth.Principal{Subject: "admin", TenantID: tenant})
	}

	t.Run("start, fail, retry", func(t *testing.T) {
		t.Parallel()
		tenant, args := seed(t, "mig-start-retry")
		if _, err := repo.StartStorageMigration(as(tenant), args); err != nil {
			t.Fatalf("start: %v", err)
		}
		if _, err := repo.StartStorageMigration(as(tenant), args); !errors.Is(err, tenanth.ErrStorageMigrationExists) {
			t.Fatalf("second start = %v, want ErrStorageMigrationExists", err)
		}
		exec(t, h, `UPDATE tenant_storage_migrations SET state = 'failed' WHERE tenant_id = $1`, tenant)
		m, err := repo.StartStorageMigration(as(tenant), args)
		if err != nil {
			t.Fatalf("retry: %v", err)
		}
		if m.State != "provisioning" {
			t.Errorf("retried migration is %s, want provisioning", m.State)
		}
	})

	// The bucket is this tenant's, but another tenant was granted use of it
	// and bound a collection there (ADR-0032). A tenant-scoped read sees no
	// such collection; the start must look past that.
	t.Run("bucket another tenant uses, hidden by RLS", func(t *testing.T) {
		t.Parallel()
		tenant, args := seed(t, "mig-start-taken")
		other := mustCreateTenant(t, h.PoolMigrate, "mig-start-other")
		mustCreateBucket(t, h.PoolMigrate, tenant, backend, args.TargetBucketName)
		mustGrantBucket(t, h.PoolMigrate, other, backend, args.TargetBucketName)
		exec(t, h, `INSERT INTO collections (tenant_id, name, bucket_id)
			SELECT $1, 'theirs', b.id FROM buckets b WHERE b.name = $2`, other, args.TargetBucketName)

		if _, err := repo.StartStorageMigration(as(tenant), args); !errors.Is(err, tenanth.ErrTargetBucketUnusable) {
			t.Fatalf("start = %v, want ErrTargetBucketUnusable", err)
		}
	})
}

func exec(t *testing.T, h *pgharness.Harness, sql string, args ...any) {
	t.Helper()
	if _, err := h.PoolMigrate.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}
