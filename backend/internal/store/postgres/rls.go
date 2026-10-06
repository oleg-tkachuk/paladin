package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// wipeTimeout caps the AfterRelease GUC reset so a stuck conn doesn't
// hold the pool's release path indefinitely. 2s is generous; the SET
// completes in microseconds when the connection is healthy.
const wipeTimeout = 2 * time.Second

// EnableRLS configures the pool so each acquired connection sets the
// `paladin.tenant_id` GUC from the request's auth context. `002_roles_and_rls.sql`
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
// The tenant comes from auth.EffectiveTenant, which is the tenant the
// request is ACTING ON — normally the caller's own, but the admin plane
// swaps it for the tenant named in the resource it was authorised to
// touch (auth.WithActingTenant). Without that swap a platform admin
// managing tenant B would write rows WITH CHECK rejects and read rows
// the policy filters away to nothing.
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
		tenantID, err := auth.EffectiveTenant(ctx)
		if err != nil || tenantID.String() == "" {
			// No tenant in ctx: zero the GUC. RLS policies will see
			// NULL and reject every row. Application code that
			// genuinely needs cross-tenant access (admin RPCs over
			// the limited set of un-RLS'd tables) runs queries
			// against tables not covered by the schema baseline (001_initial_schema.sql).
			if _, err := conn.Exec(ctx, `SELECT set_config('paladin.tenant_id', '', false)`); err != nil {
				return false, err
			}
			if err := setCrossTenant(ctx, conn, auth.CrossTenantRead(ctx)); err != nil {
				return false, err
			}
			return true, nil
		}
		if _, err := conn.Exec(ctx, `SELECT set_config('paladin.tenant_id', $1, false)`, tenantID.String()); err != nil {
			return false, err
		}
		if err := setCrossTenant(ctx, conn, auth.CrossTenantRead(ctx)); err != nil {
			return false, err
		}
		return true, nil
	}
	cfg.AfterRelease = func(conn *pgx.Conn) bool {
		// Clear the cross-tenant flag with the tenant: a connection going
		// back to the pool must not carry a widened view into the next
		// request. Failing closed here is the point.
		if _, err := conn.Exec(context.Background(),
			`SELECT set_config('paladin.cross_tenant', '', false)`); err != nil {
			return false
		}
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

// setCrossTenant flips `paladin.cross_tenant` for this connection. Always
// written, never merely set-when-true: a connection reused from the pool
// would otherwise keep the previous request's widened view.
func setCrossTenant(ctx context.Context, conn *pgx.Conn, on bool) error {
	v := ""
	if on {
		v = "on"
	}
	_, err := conn.Exec(ctx, `SELECT set_config('paladin.cross_tenant', $1, false)`, v)
	return err
}
