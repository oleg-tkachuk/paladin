//go:build integration

// RLS behaviour against a real Postgres. Validates the policies in
// migrations/023_rls.sql + the BeforeAcquire / AfterRelease hooks
// in internal/store/postgres/rls.go.
package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/tests/integration/pgharness"
)

// TestRLS_ObjectsCrossTenantSelectReturnsZero seeds two tenants with
// one row each via the BYPASSRLS migrate pool, then SELECTs through
// the app pool with the GUC stamped to tenant A. Should see exactly
// one row regardless of tenant B's data.
func TestRLS_ObjectsCrossTenantSelectReturnsZero(t *testing.T) {
	h := pgharness.Setup(t)

	tenantA := mustCreateTenant(t, h.PoolMigrate, "tenant-a")
	tenantB := mustCreateTenant(t, h.PoolMigrate, "tenant-b")

	mustCreateObjectKey(t, h.PoolMigrate, tenantA, "docs")
	mustCreateObjectKey(t, h.PoolMigrate, tenantB, "docs")

	mustInsertObject(t, h.PoolMigrate, tenantA, "docs", "key-a")
	mustInsertObject(t, h.PoolMigrate, tenantB, "docs", "key-b")

	// Double-check via BYPASSRLS pool: 2 rows total.
	if got := mustCountObjects(t, h.PoolMigrate, ""); got != 2 {
		t.Fatalf("seed: bypass pool sees %d rows, want 2", got)
	}

	// App pool with tenant A on context: 1 row visible.
	ctxA := auth.WithPrincipal(context.Background(), &auth.Principal{TenantID: tenantA})
	if got := mustCountObjects(t, h.PoolApp, ""); /*no GUC, expect 0*/ got != 0 {
		t.Errorf("no-GUC ctx: app pool sees %d rows, want 0 (RLS closed-by-default)", got)
	}
	if got := mustCountObjectsCtx(t, h.PoolApp, ctxA); got != 1 {
		t.Errorf("tenant A ctx: app pool sees %d rows, want 1", got)
	}

	// Switch GUC: app pool with tenant B on context — 1 row, the
	// other one. Cross-tenant query NEVER sees tenant A's row.
	ctxB := auth.WithPrincipal(context.Background(), &auth.Principal{TenantID: tenantB})
	if got := mustCountObjectsCtx(t, h.PoolApp, ctxB); got != 1 {
		t.Errorf("tenant B ctx: app pool sees %d rows, want 1", got)
	}
}

// TestRLS_ObjectsForceRLSAppliesToOwner — even an owner connection
// must obey RLS. paladin_app is NOT the table owner (paladin_migrate is),
// but FORCE RLS in migration 023 closes the door for the owner too.
// We can't directly test the owner path under the harness because
// the pool runs as paladin_migrate (BYPASSRLS) — but the next test
// covers the runtime invariant we actually care about.

// TestRLS_ObjectsInsertEnforcesGUCMatch: inserting with mismatched
// tenant_id (whether through the WITH CHECK on objects or via
// audit_log) is rejected.
func TestRLS_AuditLogInsertWithCheck(t *testing.T) {
	h := pgharness.Setup(t)
	tenantA := mustCreateTenant(t, h.PoolMigrate, "tenant-a")
	tenantB := mustCreateTenant(t, h.PoolMigrate, "tenant-b")

	// App pool: GUC stamped tenant A; INSERT with actor_tenant_id
	// = tenant B should be rejected by the WITH CHECK on audit_log.
	ctxA := auth.WithPrincipal(context.Background(), &auth.Principal{TenantID: tenantA})
	conn, err := h.PoolApp.Acquire(ctxA)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()

	_, err = conn.Exec(ctxA, `
        INSERT INTO audit_log (
            entry_id, at, actor_subject, actor_tenant_id, actor_audience,
            action, resource_name
        ) VALUES (
            $1, now(), 'test-actor', $2, 'data',
            'TestAction', 'test-resource'
        )
    `, uuid.New(), tenantB)
	if err == nil {
		t.Fatal("INSERT with mismatched actor_tenant_id should fail RLS WITH CHECK")
	}
	// Postgres surfaces RLS rejections as 'new row violates row-level
	// security policy'. Caller code branches on string today; integration
	// tests just verify *some* error.

	// Sanity: matched actor_tenant_id passes.
	_, err = conn.Exec(ctxA, `
        INSERT INTO audit_log (
            entry_id, at, actor_subject, actor_tenant_id, actor_audience,
            action, resource_name
        ) VALUES (
            $1, now(), 'test-actor', $2, 'data',
            'TestAction', 'test-resource'
        )
    `, uuid.New(), tenantA)
	if err != nil {
		t.Errorf("INSERT with matching actor_tenant_id should succeed, got %v", err)
	}
}

// ─── helpers ────────────────────────────────────────────────────────

func mustCreateTenant(t *testing.T, pool *pgxpool.Pool, slug string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `
        INSERT INTO tenants (tenant_id, display_name, slug)
        VALUES ($1, $2, $3)
    `, id, slug, slug)
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	return id
}

func mustCreateObjectKey(t *testing.T, pool *pgxpool.Pool, tenantID uuid.UUID, name string) {
	t.Helper()
	const backendID = "primary"
	const bucketName = "paladin-test"
	if _, err := pool.Exec(context.Background(), `
        INSERT INTO storage_backends (id, kind, region, endpoint)
        VALUES ($1, 's3-compatible', 'us-east-1', 'http://localhost')
        ON CONFLICT (id) DO NOTHING
    `, backendID); err != nil {
		t.Fatalf("seed backend: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
        INSERT INTO buckets (backend_id, bucket_name)
        VALUES ($1, $2)
        ON CONFLICT DO NOTHING
    `, backendID, bucketName); err != nil {
		t.Fatalf("seed bucket: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
        INSERT INTO object_keys (tenant_id, object_key, backend_id, bucket_name)
        VALUES ($1, $2, $3, $4)
    `, tenantID, name, backendID, bucketName); err != nil {
		t.Fatalf("create object_key: %v", err)
	}
}

func mustInsertObject(t *testing.T, pool *pgxpool.Pool, tenantID uuid.UUID, objectKey, key string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
        INSERT INTO objects (
            object_id, tenant_id, object_key, key, state,
            content_type, checksum_algorithm
        ) VALUES (
            $1, $2, $3, $4, 'AVAILABLE',
            'text/plain', 1
        )
    `, uuid.New(), tenantID, objectKey, key)
	if err != nil {
		t.Fatalf("insert object: %v", err)
	}
}

func mustCountObjects(t *testing.T, pool *pgxpool.Pool, _ string) int {
	t.Helper()
	return mustCountObjectsCtx(t, pool, context.Background())
}

func mustCountObjectsCtx(t *testing.T, pool *pgxpool.Pool, ctx context.Context) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM objects`).Scan(&n); err != nil {
		t.Fatalf("count objects: %v", err)
	}
	return n
}
