package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin-private/internal/auth"
)

// wipeTimeout caps the AfterRelease GUC reset so a stuck conn doesn't
// hold the pool's release path indefinitely. 2s is generous; the SET
// completes in microseconds when the connection is healthy.
const wipeTimeout = 2 * time.Second

// EnableRLS configures the pool so each acquired connection sets the
// `paladin.tenant_id` GUC from the request's auth context. Migration 023
// wires per-table RLS policies that key on this GUC; together they
// give us closed-by-default tenant isolation at the DB layer.
//
// PrepareConn is the right hook for two reasons:
//
//  1. It runs before pgx hands the connection to the caller (per
//     acquire, not once at connection creation), so the SET fires
//     inside the same connection acquisition that serves the query.
//
//  2. The (ctx, conn) signature lets us thread the tenant from the
//     calling request's context — which is exactly where the auth
//     interceptors stamp it.
//
// PrepareConn replaces the deprecated BeforeAcquire (same timing) and
// its (bool, error) result lets a failed GUC set surface as the real
// error on the instigating query instead of the old behaviour, which
// silently retried on other connections until "too many failed
// attempts". We return (false, err) on a failed SET: the connection
// is suspect, so destroy it and fail the query with the cause.
//
// AfterRelease wipes the GUC back to ” so a connection returning
// to the pool doesn't leak its previous tenant on the next checkout
// if (somehow) PrepareConn is bypassed. The policy's "NULL ⇒ no
// rows" rule means a wiped GUC fails closed.
//
// Worker / migrate paths run as `paladin_migrate` (BYPASSRLS) so they
// don't need to set the GUC. Application paths run as `paladin_app` —
// queries without the GUC return zero rows, which surfaces the
// misconfig immediately.
//
// Returns the modified config so the caller's NewWithConfig picks
// it up. Caller passes a fresh pgxpool.Config.
func EnableRLS(cfg *pgxpool.Config) *pgxpool.Config {
	cfg.PrepareConn = func(ctx context.Context, conn *pgx.Conn) (bool, error) {
		tenantID, err := auth.TenantFromContext(ctx)
		if err != nil || tenantID.String() == "" {
			// No tenant in ctx: zero the GUC. RLS policies will see
			// NULL and reject every row. Application code that
			// genuinely needs cross-tenant access (admin RPCs over
			// the limited set of un-RLS'd tables) runs queries
			// against tables not covered by migration 023.
			if _, err := conn.Exec(ctx, `SELECT set_config('paladin.tenant_id', '', false)`); err != nil {
				return false, err
			}
			return true, nil
		}
		if _, err := conn.Exec(ctx, `SELECT set_config('paladin.tenant_id', $1, false)`, tenantID.String()); err != nil {
			return false, err
		}
		return true, nil
	}
	cfg.AfterRelease = func(conn *pgx.Conn) bool {
		// Wipe the GUC on release so a connection returning to the
		// pool doesn't carry tenant context for a request that
		// somehow skipped PrepareConn. Pool keeps the connection
		// only when this returns true.
		ctx, cancel := context.WithTimeout(context.Background(), wipeTimeout)
		defer cancel()
		_, err := conn.Exec(ctx, `SELECT set_config('paladin.tenant_id', '', false)`)
		return err == nil
	}
	return cfg
}
