//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestRLSTenantIsolation proves the migration 023/024 row-level-security
// policies actually enforce tenant isolation at the DB layer — the
// defence-in-depth guarantee the app-level Cedar checks sit on top of.
//
// The testcontainer connects as the bootstrap superuser (which bypasses RLS
// unconditionally), so every case runs inside a tx that `SET LOCAL ROLE`s to
// a restricted, non-BYPASSRLS role mirroring the runtime's `paladin_app`, and
// sets the `paladin.tenant_id` GUC exactly as internal/store/postgres/rls.go's
// PrepareConn hook does per connection acquisition.
func TestRLSTenantIsolation(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)

	// Restricted runtime role. NOSUPERUSER + NOBYPASSRLS is the whole point
	// — without it RLS is silently skipped. The migrations may already
	// provision paladin_app; create-if-absent, then ALTER to assert the
	// attributes the test depends on. Grant the DML the app needs.
	mustExec(t, ctx, pool, `DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'paladin_app') THEN
			CREATE ROLE paladin_app NOSUPERUSER NOBYPASSRLS;
		END IF;
	END $$`)
	mustExec(t, ctx, pool, `ALTER ROLE paladin_app NOSUPERUSER NOBYPASSRLS`)
	mustExec(t, ctx, pool, `GRANT USAGE ON SCHEMA public TO paladin_app`)
	mustExec(t, ctx, pool, `GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO paladin_app`)

	// Two tenants, one object each — seeded as superuser (bypasses RLS).
	fA := seedFixture(t, ctx, pool)
	fB := seedFixture(t, ctx, pool)
	objA := seedPendingObject(t, ctx, pool, fA)
	objB := seedPendingObject(t, ctx, pool, fB)

	// asApp runs fn in a tx where current_user = paladin_app and paladin.tenant_id =
	// tenantGUC. Always rolled back, so cases don't bleed into each other
	// (and a WITH CHECK violation that aborts the tx is contained).
	asApp := func(t *testing.T, tenantGUC string, fn func(t *testing.T, tx pgx.Tx)) {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE paladin_app`); err != nil {
			t.Fatalf("set role: %v", err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('paladin.tenant_id', $1, true)`, tenantGUC); err != nil {
			t.Fatalf("set guc: %v", err)
		}
		fn(t, tx)
	}

	objVisible := func(t *testing.T, tx pgx.Tx, id uuid.UUID) bool {
		t.Helper()
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM objects WHERE object_id = $1`, id).Scan(&n); err != nil {
			t.Fatalf("count objects: %v", err)
		}
		return n == 1
	}
	totalObjects := func(t *testing.T, tx pgx.Tx) int {
		t.Helper()
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM objects`).Scan(&n); err != nil {
			t.Fatalf("count objects: %v", err)
		}
		return n
	}

	// ── read isolation: each tenant's GUC sees only its own rows ─────────
	t.Run("GUC=A sees A's object, not B's", func(t *testing.T) {
		asApp(t, fA.tenantID.String(), func(t *testing.T, tx pgx.Tx) {
			if !objVisible(t, tx, objA) {
				t.Error("A's object not visible under GUC=A")
			}
			if objVisible(t, tx, objB) {
				t.Error("B's object LEAKED under GUC=A")
			}
			if n := totalObjects(t, tx); n != 1 {
				t.Errorf("total objects = %d, want 1 (only A's)", n)
			}
		})
	})

	t.Run("GUC=B sees B's object, not A's", func(t *testing.T) {
		asApp(t, fB.tenantID.String(), func(t *testing.T, tx pgx.Tx) {
			if !objVisible(t, tx, objB) {
				t.Error("B's object not visible under GUC=B")
			}
			if objVisible(t, tx, objA) {
				t.Error("A's object LEAKED under GUC=B")
			}
		})
	})

	// ── closed-by-default: unset / foreign GUC sees nothing ──────────────
	t.Run("unset GUC sees zero rows", func(t *testing.T) {
		asApp(t, "", func(t *testing.T, tx pgx.Tx) {
			if n := totalObjects(t, tx); n != 0 {
				t.Errorf("unset GUC saw %d rows, want 0 (closed-by-default)", n)
			}
		})
	})

	t.Run("foreign-tenant GUC sees zero rows", func(t *testing.T) {
		asApp(t, uuid.NewString(), func(t *testing.T, tx pgx.Tx) {
			if n := totalObjects(t, tx); n != 0 {
				t.Errorf("foreign GUC saw %d rows, want 0", n)
			}
		})
	})

	// ── write isolation (WITH CHECK): can't stamp another tenant's row ───
	t.Run("WITH CHECK rejects cross-tenant INSERT", func(t *testing.T) {
		asApp(t, fA.tenantID.String(), func(t *testing.T, tx pgx.Tx) {
			// A valid FK chain for tenant B exists (fB seeded it), so only
			// the RLS WITH CHECK (tenant_id must equal the GUC) can reject
			// this — proving the policy, not a constraint, is the gate.
			_, err := tx.Exec(ctx,
				`INSERT INTO objects (object_id, tenant_id, collection, key, state, content_type, checksum_algorithm)
				 VALUES ($1, $2, $3, 'rls-probe', 'PENDING', 'application/octet-stream', 0)`,
				uuid.Must(uuid.NewV7()), fB.tenantID, fB.collection)
			if err == nil {
				t.Fatal("cross-tenant INSERT succeeded — WITH CHECK not enforced")
			}
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
				t.Fatalf("want RLS violation (SQLSTATE 42501), got %v", err)
			}
		})
	})

	t.Run("WITH CHECK allows own-tenant INSERT", func(t *testing.T) {
		asApp(t, fA.tenantID.String(), func(t *testing.T, tx pgx.Tx) {
			_, err := tx.Exec(ctx,
				`INSERT INTO objects (object_id, tenant_id, collection, key, state, content_type, checksum_algorithm)
				 VALUES ($1, $2, $3, 'rls-ok', 'PENDING', 'application/octet-stream', 0)`,
				uuid.Must(uuid.NewV7()), fA.tenantID, fA.collection)
			if err != nil {
				t.Fatalf("own-tenant INSERT rejected: %v", err)
			}
		})
	})

	// ── audit_log: the 024 "free cross-tenant read, tenant-stamped write"
	//    shape (compliance reads span tenants; INSERT must match the actor).
	seedAudit := func(t *testing.T, actorTenant uuid.UUID) {
		t.Helper()
		mustExec(t, ctx, pool,
			`INSERT INTO audit_log (entry_id, actor_subject, actor_tenant_id, actor_audience, action, resource_name)
			 VALUES ($1, 'svc', $2, 'admin', 'Test', 'r')`,
			uuid.Must(uuid.NewV7()), actorTenant)
	}
	seedAudit(t, fA.tenantID)
	seedAudit(t, fB.tenantID)

	t.Run("audit_log: cross-tenant SELECT is free", func(t *testing.T) {
		asApp(t, fA.tenantID.String(), func(t *testing.T, tx pgx.Tx) {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(&n); err != nil {
				t.Fatalf("count audit_log: %v", err)
			}
			if n < 2 {
				t.Errorf("audit_log visible rows = %d, want ≥2 (free cross-tenant read)", n)
			}
		})
	})

	t.Run("audit_log: INSERT stamping another tenant is rejected", func(t *testing.T) {
		asApp(t, fA.tenantID.String(), func(t *testing.T, tx pgx.Tx) {
			_, err := tx.Exec(ctx,
				`INSERT INTO audit_log (entry_id, actor_subject, actor_tenant_id, actor_audience, action, resource_name)
				 VALUES ($1, 'svc', $2, 'admin', 'Test', 'r')`,
				uuid.Must(uuid.NewV7()), fB.tenantID)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
				t.Fatalf("want RLS violation (42501) on wrong actor_tenant_id, got %v", err)
			}
		})
	})
}
