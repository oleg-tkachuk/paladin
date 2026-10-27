//go:build integration

package components

import (
	"context"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
)

// beforeOnePerTenant is the last migration that allowed a tenant more than one
// storage migration; 062 removes the duplicates and 063 forbids them.
const beforeOnePerTenant = 61

// A database migrated before 062 may hold two storage migrations for one
// tenant. Migrating it keeps the latest — the one every reader already took
// as the tenant's — and leaves tenant_id unique.
func TestStorageMigrationsBecomeOnePerTenant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgresAt(t, beforeOnePerTenant)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })

	tenant, _ := mkTenant(t, ctx, pool, "shared")
	single, _ := mkTenant(t, ctx, pool, "shared")
	mustExec(t, ctx, pool,
		`INSERT INTO storage_backends (name, kind) VALUES ('uniq-be', 's3-compatible')`)
	// Owned, so migrating up past 080 has no ownerless bucket to hand to a
	// platform tenant this database does not have (ADR-0032).
	mustExec(t, ctx, pool, `
		INSERT INTO buckets (backend_id, name, owner_tenant_id)
		SELECT sb.id, b, $1 FROM storage_backends sb, unnest(ARRAY['uniq-src', 'uniq-dst']) b
		 WHERE sb.name = 'uniq-be'`, tenant)
	insert := func(tenant uuid.UUID, state string, created time.Time) uuid.UUID {
		t.Helper()
		id := uuid.New()
		mustExec(t, ctx, pool, `
			INSERT INTO tenant_storage_migrations
			       (id, tenant_id, source_bucket_id, target_bucket_id, state, created_at)
			SELECT $1, $2, s.id, d.id, $3, $4
			  FROM buckets s, buckets d
			 WHERE s.name = 'uniq-src' AND d.name = 'uniq-dst'`, id, tenant, state, created)
		return id
	}
	now := time.Now().UTC()
	insert(tenant, "copying", now.Add(-2*time.Hour))
	insert(tenant, "failed", now.Add(-time.Hour))
	latest := insert(tenant, "provisioning", now)
	only := insert(single, "completed", now.Add(-time.Hour))

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	rows, err := pool.Query(ctx,
		`SELECT tenant_id, id FROM tenant_storage_migrations WHERE tenant_id = ANY($1)`,
		[]uuid.UUID{tenant, single})
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	kept := map[uuid.UUID][]uuid.UUID{}
	for rows.Next() {
		var tid, id uuid.UUID
		if err := rows.Scan(&tid, &id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		kept[tid] = append(kept[tid], id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if got := kept[tenant]; len(got) != 1 || got[0] != latest {
		t.Errorf("tenant with three migrations kept %v, want only the latest %s", got, latest)
	}
	if got := kept[single]; len(got) != 1 || got[0] != only {
		t.Errorf("tenant with one migration kept %v, want %s", got, only)
	}

	// The schema now refuses a second migration for the tenant.
	_, err = pool.Exec(ctx, `
		INSERT INTO tenant_storage_migrations (tenant_id, source_bucket_id, target_bucket_id, state)
		SELECT $1, s.id, d.id, 'provisioning'
		  FROM buckets s, buckets d
		 WHERE s.name = 'uniq-src' AND d.name = 'uniq-dst'`, tenant)
	if !pgerr.Is(err, pgerr.UniqueViolation) {
		t.Errorf("second migration for one tenant: %v, want a unique violation", err)
	}
	var plainIndexes int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_indexes
		 WHERE tablename = 'tenant_storage_migrations'
		   AND indexname = 'tenant_storage_migrations_tenant_idx'`).Scan(&plainIndexes); err != nil {
		t.Fatalf("read indexes: %v", err)
	}
	if plainIndexes != 0 {
		t.Error("the plain tenant_id index is still there beside the unique one")
	}
}
