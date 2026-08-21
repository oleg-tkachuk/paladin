//go:build integration

// Row-level security is Paladin's primary isolation control, not a second
// layer behind an application check. That makes an unprotected table a hole
// rather than a missed optimisation — and the way one appears is not a bad
// decision but an absent one: a table gets added, nobody writes a policy, and
// nothing says so.
//
// capability_revocations was exactly that. It had no policy, and Store.Revoke
// keyed its INSERT on a caller-supplied uuid, so one tenant could revoke
// another's credentials. Nothing in the codebase distinguished that omission
// from the deliberate exemptions sitting next to it.
//
// This gate asks Postgres, not the source: every table with a tenant_id
// column must either carry a policy or be named in the allow-list below, with
// a reason. Adding a table without either fails here.
package integration

import (
	"context"
	"sort"
	"testing"

	"github.com/oleg-tkachuk/paladin/tests/integration/pgharness"
)

// exemptFromRLS names every table that carries tenant_id and deliberately has
// no policy. An entry is a claim that someone thought about it — the string is
// the reason, and it belongs in the failure message when the list is wrong.
var exemptFromRLS = map[string]string{
	// Read before the tenant is known — the row is what identifies it. RLS
	// cannot scope a lookup whose whole purpose is to discover the scope.
	// Both are gated in the application instead, and both gates are real:
	//
	//   oauth_authorization_codes — ConsumeCode matches on a hash of the
	//   code and stamps consumed_at in the same statement, so possession of
	//   the secret is the authorisation and a replay finds no row.
	//
	//   tenant_slug_history — ResolveRenamedSlug looks the old slug up
	//   across all tenants, then authorises ReadTenant against the tenant it
	//   resolved to, collapsing every failure (absent, expired, denied) to
	//   NotFound so the endpoint cannot be used to enumerate slugs.
	//
	// api_tokens has the same constraint and solves it with a second,
	// grant-narrowed pre-auth policy. These two could follow that pattern;
	// neither is a hole today.
	"oauth_authorization_codes": "pre-auth: the code hash is the credential; ConsumeCode is single-use and atomic",
	"tenant_slug_history":       "pre-auth: ResolveRenamedSlug authorises against the resolved tenant, not the query",

	// Open questions, recorded in BACKLOG.md under "Tables carrying
	// tenant_id with no RLS policy". Listed here so the gate passes today
	// and so that deleting an entry is the visible act of closing the gap.
	"users":                   "BACKLOG: login reads the row before the tenant is known; needs the api_tokens pre-auth pattern",
	"refresh_tokens":          "BACKLOG: refresh happens pre-session, same shape as users",
	"user_settings":           "BACKLOG: subordinate to users; policy waits on the users decision",
	"tenant_default_bindings": "BACKLOG: written by the admin plane on behalf of another tenant",
}

// TestEveryTenantScopedTableHasAPolicy enumerates tables with a tenant_id
// column and no row-level-security policy.
func TestEveryTenantScopedTableHasAPolicy(t *testing.T) {
	ctx := context.Background()
	pool := pgharness.Setup(t).PoolMigrate

	// Partitions inherit their parent's policies, and pg_policies lists the
	// policy only against the parent, so a partition would otherwise read as
	// unprotected. pg_inherits filters them out.
	const q = `
SELECT DISTINCT c.relname
FROM   pg_class c
JOIN   pg_namespace n ON n.oid = c.relnamespace
JOIN   pg_attribute a ON a.attrelid = c.oid
WHERE  n.nspname = 'public'
  AND  c.relkind IN ('r', 'p')
  AND  a.attname = 'tenant_id'
  AND  a.attnum > 0 AND NOT a.attisdropped
  AND  NOT EXISTS (SELECT 1 FROM pg_policies p
                   WHERE p.schemaname = 'public' AND p.tablename = c.relname)
  AND  NOT EXISTS (SELECT 1 FROM pg_inherits i WHERE i.inhrelid = c.oid)
ORDER  BY c.relname;
`
	rows, err := pool.Query(ctx, q)
	if err != nil {
		t.Fatalf("query unprotected tables: %v", err)
	}
	defer rows.Close()

	var unprotected []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		unprotected = append(unprotected, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}

	// Guard against the query silently matching nothing — if the schema
	// stopped using tenant_id, or the join broke, an empty result would read
	// as success.
	var scoped int
	if err := pool.QueryRow(ctx, `
SELECT count(DISTINCT c.relname)
FROM   pg_class c
JOIN   pg_namespace n ON n.oid = c.relnamespace
JOIN   pg_attribute a ON a.attrelid = c.oid
WHERE  n.nspname = 'public' AND c.relkind IN ('r','p')
  AND  a.attname = 'tenant_id' AND a.attnum > 0 AND NOT a.attisdropped`).Scan(&scoped); err != nil {
		t.Fatalf("count tenant-scoped tables: %v", err)
	}
	if scoped < 10 {
		t.Fatalf("only %d tables carry tenant_id — the probe is broken, not the schema", scoped)
	}
	t.Logf("%d tenant-scoped tables, %d without a policy, %d exempt",
		scoped, len(unprotected), len(exemptFromRLS))

	for _, name := range unprotected {
		if _, ok := exemptFromRLS[name]; !ok {
			t.Errorf("table %q has a tenant_id column and no RLS policy.\n"+
				"  RLS is the primary isolation control here, so this is a cross-tenant hole\n"+
				"  unless it is deliberate. Add a policy, or add %q to exemptFromRLS with the\n"+
				"  reason and a BACKLOG entry.", name, name)
		}
	}

	// The allow-list has to shrink as gaps close. An entry naming a table that
	// now has a policy — or no longer exists — is stale documentation that
	// reads as an active exemption.
	var stale []string
	for name := range exemptFromRLS {
		found := false
		for _, u := range unprotected {
			if u == name {
				found = true
				break
			}
		}
		if !found {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	for _, name := range stale {
		t.Errorf("table %q is listed in exemptFromRLS but is no longer unprotected "+
			"(it has a policy now, or the table is gone) — remove the entry", name)
	}
}
