//go:build integration

package components

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/platformstats"
)

// Both credential censuses state the same contract in a comment and enforce
// it nowhere:
//
//	Active / Expired / Revoked are mutually exclusive and sum to Total: a
//	revocation row wins over expiry (an operator who revoked a token wants to
//	see it counted as revoked, not quietly reclassified when it lapses), and
//	expiry wins over active.
//
// Three FILTER clauses can drift into overlapping or leaving a gap without
// any of them erroring: a row counted twice inflates the page, a row counted
// nowhere makes it silently under-report. The sum identity is the thing that
// notices, so it is asserted on the absolute counts rather than on deltas —
// it must hold for the whole table, not just for what this test added.
//
// The rows that carry the weight are the ones that are BOTH revoked and
// expired. Under the contract they are revoked; a filter written the obvious
// way counts them as expired too.
//
// This does not touch the boundary operators — `expires_at > now()` versus
// `>=` differs only for a row expiring in the exact microsecond the census
// reads the clock, and no test can place a row there. See BACKLOG.
func TestCredentialCensuses_ClassifyEachRowExactlyOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)

	base, err := platformstats.CollectRLS(ctx, pool, platformstats.TenantPage{})
	if err != nil {
		t.Fatalf("baseline census: %v", err)
	}

	hex := uuid.NewString()[:8]
	tenantID := uuid.New()
	mustExec(t, ctx, pool, `INSERT INTO tenants (id, slug, display_name) VALUES ($1, $2, $3)`,
		tenantID, "t-"+hex, "tn-"+hex)

	// ─── capabilities ───────────────────────────────────────────────────
	issue := func(kind, expires string, parent *uuid.UUID) uuid.UUID {
		t.Helper()
		id := uuid.New()
		mustExec(t, ctx, pool, `
			INSERT INTO capability_records
				(id, tenant_id, parent_id, issuer, principal_kind, principal_subject,
				 audience, caveats, created_by, expires_at)
			VALUES ($1, $2, $3, 'test', $4, 'subj', ARRAY['paladin-data'], '{}'::jsonb,
			        'test', now() + $5::interval)`,
			id, tenantID, parent, kind, expires)
		return id
	}
	revoke := func(id uuid.UUID) {
		t.Helper()
		mustExec(t, ctx, pool, `INSERT INTO capability_revocations (id) VALUES ($1)`, id)
	}

	live := issue("agent", "30 days", nil) // active, not expiring soon
	issue("agent", "1 hour", nil)          // active AND expiring within 24h
	issue("agent", "-1 hour", nil)         // expired, never revoked
	revoke(issue("agent", "30 days", nil)) // revoked while still valid
	revoke(issue("agent", "-1 hour", nil)) // revoked AND expired — revocation wins
	issue("service", "30 days", &live)     // active, delegated from `live`

	// ─── api tokens ─────────────────────────────────────────────────────
	tok := func(expires string, lastUsed, revoked bool) {
		t.Helper()
		mustExec(t, ctx, pool, `
			INSERT INTO api_tokens
				(tenant_id, name, prefix, audience, created_by, expires_at,
				 last_used_at, revoked_at)
			VALUES ($1, $2, $3, ARRAY['paladin-data'], 'test', now() + $4::interval,
			        CASE WHEN $5::bool THEN now() END,
			        CASE WHEN $6::bool THEN now() END)`,
			tenantID, "tok-"+uuid.NewString()[:8], uuid.NewString()[:8],
			expires, lastUsed, revoked)
	}

	tok("30 days", true, false)  // active, used, not expiring soon
	tok("1 day", false, false)   // active, never used, expiring within 7 days
	tok("-1 hour", false, false) // expired and never used — NOT "never used",
	// which counts active rows only
	tok("30 days", false, true) // revoked while still valid, never used
	tok("-1 hour", false, true) // revoked AND expired — revocation wins

	got, err := platformstats.CollectRLS(ctx, pool, platformstats.TenantPage{})
	if err != nil {
		t.Fatalf("CollectRLS: %v", err)
	}

	c, bc := got.Capabilities, base.Capabilities
	assertDelta(t, "capabilities.total", bc.Total, c.Total, 6)
	assertDelta(t, "capabilities.active", bc.Active, c.Active, 3)
	assertDelta(t, "capabilities.expired", bc.Expired, c.Expired, 1)
	assertDelta(t, "capabilities.revoked", bc.Revoked, c.Revoked, 2)
	assertDelta(t, "capabilities.delegated", bc.Delegated, c.Delegated, 1)
	assertDelta(t, "capabilities.expiring_soon", bc.ExpiringSoon, c.ExpiringSoon, 1)
	// ByPrincipalKind covers ACTIVE rows only, which is what the three
	// non-active "agent" rows above are there to prove.
	assertDelta(t, "capabilities.by_kind[agent]",
		bc.ByPrincipalKind["agent"], c.ByPrincipalKind["agent"], 2)
	assertDelta(t, "capabilities.by_kind[service]",
		bc.ByPrincipalKind["service"], c.ByPrincipalKind["service"], 1)

	a, ba := got.APITokens, base.APITokens
	assertDelta(t, "api_tokens.total", ba.Total, a.Total, 5)
	assertDelta(t, "api_tokens.active", ba.Active, a.Active, 2)
	assertDelta(t, "api_tokens.expired", ba.Expired, a.Expired, 1)
	assertDelta(t, "api_tokens.revoked", ba.Revoked, a.Revoked, 2)
	assertDelta(t, "api_tokens.expiring_soon", ba.ExpiringSoon, a.ExpiringSoon, 1)
	assertDelta(t, "api_tokens.never_used", ba.NeverUsed, a.NeverUsed, 1)

	// The identity the comment promises, over the whole table.
	if sum := c.Active + c.Expired + c.Revoked; sum != c.Total {
		t.Errorf("capabilities: active+expired+revoked = %d, total = %d — the three "+
			"filters overlap or leave a gap, so the page double-counts or "+
			"under-reports with nothing to signal it", sum, c.Total)
	}
	if sum := a.Active + a.Expired + a.Revoked; sum != a.Total {
		t.Errorf("api_tokens: active+expired+revoked = %d, total = %d — same",
			sum, a.Total)
	}
}
