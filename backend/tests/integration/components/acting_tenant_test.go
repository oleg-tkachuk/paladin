//go:build integration

// The admin plane manages resources that belong to someone else. RLS scopes
// every connection to one tenant, so acting on another tenant's row requires
// swapping that scope — auth.WithActingTenant, read by the RLS pool.
//
// Both directions matter and they fail differently:
//   - a cross-tenant WRITE without the swap trips WITH CHECK and errors;
//   - a cross-tenant READ without it returns zero rows and does not error,
//     because RLS filters. That is the failure that shipped: the console
//     rendered an empty capability list over a populated table.
//
// This test pins both, plus the property that makes the mechanism safe: it
// grants access to exactly one tenant, not to all of them.
package components

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
)

func TestActingTenantScopesRLS(t *testing.T) {
	ctx := context.Background()
	admin := startPostgres(t)

	tenantA, _ := mkTenant(t, ctx, admin, "shared")
	tenantB, _ := mkTenant(t, ctx, admin, "shared")
	seedBucketRow(t, ctx, admin)
	seedCollectionFor(t, ctx, admin, tenantA, "docs-a")
	seedCollectionFor(t, ctx, admin, tenantB, "docs-b")

	// A pool that behaves like the runtime's: RLS on, no BYPASSRLS.
	pool := rlsPool(t, ctx, admin)

	caller := &auth.Principal{TenantID: tenantA}
	base := auth.WithPrincipal(ctx, caller)

	count := func(c context.Context) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(c, `SELECT count(*) FROM collections`).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	// Without the swap the caller sees only its own tenant's row.
	if got := count(base); got != 1 {
		t.Errorf("caller's own scope sees %d collections, want 1", got)
	}

	// Acting as B, it sees B's — and still only one, not both.
	actingB := auth.WithActingTenant(base, tenantB)
	if got := count(actingB); got != 1 {
		t.Errorf("acting as B sees %d collections, want 1", got)
	}
	var name string
	if err := pool.QueryRow(actingB, `SELECT name FROM collections`).Scan(&name); err != nil {
		t.Fatalf("read as B: %v", err)
	}
	if name != "docs-b" {
		t.Errorf("acting as B read %q, want docs-b — the scope did not move", name)
	}

	// The write side: inserting B's row from A's context must be rejected
	// without the swap, and accepted with it.
	insert := func(c context.Context, tenant uuid.UUID, collection string) error {
		_, err := pool.Exec(c, `
			INSERT INTO collections (tenant_id, name, bucket_id)
			SELECT $1, $2, b.id FROM buckets b LIMIT 1`, tenant, collection)
		return err
	}
	if err := insert(base, tenantB, "sneaky"); err == nil {
		t.Error("SECURITY: wrote a row for tenant B from tenant A's scope")
	}
	if err := insert(actingB, tenantB, "legit-b"); err != nil {
		t.Errorf("acting as B could not write B's own row: %v", err)
	}

	// Acting as B grants B and nothing more: A's rows are now invisible.
	var visible int
	if err := pool.QueryRow(actingB,
		`SELECT count(*) FROM collections WHERE tenant_id = $1`, tenantA).Scan(&visible); err != nil {
		t.Fatalf("count A from B: %v", err)
	}
	if visible != 0 {
		t.Errorf("acting as B still sees %d of A's collections — the swap widened access "+
			"instead of moving it", visible)
	}
}

// rlsPool opens a second pool on the same database as a NOBYPASSRLS role,
// mirroring how the runtime connects. The suite's own pool is a superuser,
// for which RLS never engages — which is exactly why this class of bug
// reached a cluster before a test saw it.
func rlsPool(t *testing.T, ctx context.Context, admin *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	const role = "paladin_app_rlstest"
	mustExec(t, ctx, admin, `DROP ROLE IF EXISTS `+role)
	mustExec(t, ctx, admin, `CREATE ROLE `+role+` LOGIN PASSWORD 'x' NOBYPASSRLS`)
	mustExec(t, ctx, admin, `GRANT USAGE ON SCHEMA public TO `+role)
	mustExec(t, ctx, admin, `GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO `+role)

	cfg := admin.Config().Copy()
	cfg.ConnConfig.User = role
	cfg.ConnConfig.Password = "x"
	p, err := pgxpool.NewWithConfig(ctx, postgres.EnableRLS(cfg))
	if err != nil {
		t.Fatalf("rls pool: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

func seedBucketRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	mustExec(t, ctx, pool,
		`INSERT INTO storage_backends (name, kind) VALUES ('acting-be', 's3-compatible')
		 ON CONFLICT (name) DO NOTHING`)
	mustExec(t, ctx, pool,
		`INSERT INTO buckets (backend_id, name)
		 SELECT sb.id, 'acting-bucket' FROM storage_backends sb WHERE sb.name = 'acting-be'
		 ON CONFLICT DO NOTHING`)
}

func seedCollectionFor(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant uuid.UUID, name string) {
	t.Helper()
	mustExec(t, ctx, pool,
		`INSERT INTO collections (tenant_id, name, bucket_id)
		 SELECT $1, $2, b.id FROM buckets b LIMIT 1`, tenant, name)
}

// TestCrossTenantReadWidensSelectOnly pins the asymmetry that makes the
// escape hatch safe: the flag admits a platform-wide SELECT and has no
// effect on writes, because the policies consult it in USING and never in
// WITH CHECK. A misplaced flag can therefore show too much — never
// cross-write, which is the failure that would actually corrupt data.
func TestCrossTenantReadWidensSelectOnly(t *testing.T) {
	ctx := context.Background()
	admin := startPostgres(t)

	tenantA, _ := mkTenant(t, ctx, admin, "shared")
	tenantB, _ := mkTenant(t, ctx, admin, "shared")
	seedBucketRow(t, ctx, admin)
	seedCollectionFor(t, ctx, admin, tenantA, "docs-a")
	seedCollectionFor(t, ctx, admin, tenantB, "docs-b")

	pool := rlsPool(t, ctx, admin)
	base := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenantA})

	count := func(c context.Context) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(c, `SELECT count(*) FROM collections`).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	if got := count(base); got != 1 {
		t.Errorf("without the flag the caller sees %d collections, want 1", got)
	}
	crossed := auth.WithCrossTenantRead(base)
	if got := count(crossed); got != 2 {
		t.Errorf("with the flag the caller sees %d collections, want both", got)
	}

	// The write side is unmoved: WITH CHECK still pins the row to the
	// connection's tenant, flag or no flag.
	_, err := pool.Exec(crossed, `
		INSERT INTO collections (tenant_id, name, bucket_id)
		SELECT $1, 'smuggled', b.id FROM buckets b LIMIT 1`, tenantB)
	if err == nil {
		t.Error("SECURITY: the cross-tenant READ flag also permitted a write")
	}

	// And it does not survive the connection going back to the pool: a
	// later request without the flag must see its own tenant again.
	if got := count(base); got != 1 {
		t.Errorf("after a flagged query the plain scope sees %d, want 1 — "+
			"the flag leaked across a checkout", got)
	}
}
