//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestAPITokensPreAuthLookup is the regression test whose absence let the
// api_tokens RLS bug ship: the RLS baseline (002_roles_and_rls.sql) placed api_tokens under a plain
// tenant_isolation policy, which filtered the PRE-AUTH verify lookup to zero
// rows (the token is what establishes the tenant, so no paladin.tenant_id GUC is
// set yet) — breaking data-plane API-token auth for every tenant.
//
// Migration 060 keeps RLS ON for writes (tenant_isolation WITH CHECK) but adds
// a permissive SELECT policy so ONLY the read path — the unavoidable
// tenant-less digest lookup — is open. This test locks that access pattern:
// reads work with no tenant GUC; writes still require the matching tenant.
func TestAPITokensPreAuthLookup(t *testing.T) {
	// Not parallel: ALTERs the server-wide paladin_app role, and concurrent updates of one role row fail with "tuple concurrently updated".
	ctx := context.Background()
	pool := startPostgres(t)

	// Mirror the runtime role: NOSUPERUSER + NOBYPASSRLS so RLS is enforced
	// (a superuser or BYPASSRLS role would skip policies and hide the bug).
	mustExec(t, ctx, pool, `DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'paladin_app') THEN
			CREATE ROLE paladin_app NOSUPERUSER NOBYPASSRLS;
		END IF;
	END $$`)
	mustExec(t, ctx, pool, `ALTER ROLE paladin_app NOSUPERUSER NOBYPASSRLS`)
	mustExec(t, ctx, pool, `GRANT USAGE ON SCHEMA public TO paladin_app`)
	mustExec(t, ctx, pool, `GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO paladin_app`)

	fA := seedFixture(t, ctx, pool)
	fB := seedFixture(t, ctx, pool)

	// Seed one token for tenant A (as superuser; bypasses RLS for setup).
	digestA := []byte("hmac-digest-A-0123456789abcdef0123456789") // 32-byte-ish bytea
	tokA := uuid.New()
	mustExec(t, ctx, pool, `INSERT INTO api_tokens
		(id, tenant_id, name, prefix, token_hmac, scopes, audience, expires_at, created_by)
		VALUES ($1, $2, 'svc-a', 'AAAAAAAA', $3, '{}'::text[], '{data}'::text[], now() + interval '1 year', 'test')`,
		tokA, fA.tenantID, digestA)

	// asApp runs fn as paladin_app with paladin.tenant_id = tenantGUC (empty string =
	// the pre-auth state). Always rolled back so cases don't bleed.
	asApp := func(t *testing.T, tenantGUC string, fn func(tx pgx.Tx)) {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE paladin_app`); err != nil {
			t.Fatalf("set role: %v", err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('paladin.tenant_id', $1, true)`, tenantGUC); err != nil {
			t.Fatalf("set guc: %v", err)
		}
		fn(tx)
	}

	countByDigest := func(t *testing.T, tx pgx.Tx, d []byte) int {
		t.Helper()
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM api_tokens WHERE token_hmac = $1`, d).Scan(&n); err != nil {
			t.Fatalf("count by digest: %v", err)
		}
		return n
	}

	// (1) THE REGRESSION: pre-auth lookup with NO tenant GUC must see the row.
	asApp(t, "", func(tx pgx.Tx) {
		if n := countByDigest(t, tx, digestA); n != 1 {
			t.Fatalf("pre-auth digest lookup (empty GUC): got %d rows, want 1 — RLS is filtering the verify path", n)
		}
	})

	// (2) A tenant with its own GUC set also reads its token (tenant_isolation
	// OR the permissive read — either way visible).
	asApp(t, fA.tenantID.String(), func(tx pgx.Tx) {
		if n := countByDigest(t, tx, digestA); n != 1 {
			t.Fatalf("tenant-A lookup: got %d rows, want 1", n)
		}
	})

	// (3) WRITES STAY ISOLATED: inserting a token for tenant A while scoped to
	// tenant B must be rejected by tenant_isolation's WITH CHECK.
	asApp(t, fB.tenantID.String(), func(tx pgx.Tx) {
		_, err := tx.Exec(ctx, `INSERT INTO api_tokens
			(id, tenant_id, name, prefix, token_hmac, scopes, audience, expires_at, created_by)
			VALUES ($1, $2, 'evil', 'BBBBBBBB', $3, '{}'::text[], '{data}'::text[], now() + interval '1 year', 'test')`,
			uuid.New(), fA.tenantID, []byte("hmac-digest-B-cross-tenant-write-000000"))
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("cross-tenant INSERT: want RLS violation (42501), got %v", err)
		}
	})

	// (4) A write with NO tenant GUC (the verify path) cannot mutate rows —
	// the UPDATE is tenant-filtered to zero rows. This is why TouchLastUsed
	// sets the tenant GUC itself.
	asApp(t, "", func(tx pgx.Tx) {
		tag, err := tx.Exec(ctx, `UPDATE api_tokens SET last_used_at = $1 WHERE id = $2`, time.Now(), tokA)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if tag.RowsAffected() != 0 {
			t.Fatalf("update with empty GUC affected %d rows, want 0 (writes must be tenant-scoped)", tag.RowsAffected())
		}
	})
}
