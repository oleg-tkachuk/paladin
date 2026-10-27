//go:build integration

package components

import (
	"context"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/limes"
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

	hex := uuid.New().String()[:8]
	tenantID := uuid.New()
	mustExec(t, ctx, pool, `INSERT INTO tenants (id, slug, display_name) VALUES ($1, $2, $3)`,
		tenantID, "t-"+hex, "tn-"+hex)

	// ─── capabilities ───────────────────────────────────────────────────
	now := time.Now()
	issue := func(kind limes.PrincipalType, expires time.Time, parent uuid.UUID) uuid.UUID {
		t.Helper()
		return seedCapability(t, ctx, pool, tenantID, kind, expires, parent)
	}
	revoke := func(id uuid.UUID) { seedRevocation(t, ctx, pool, id) }
	month, hour := now.Add(30*24*time.Hour), now.Add(time.Hour)
	lapsed := now.Add(-time.Hour)

	live := issue(limes.PrincipalTypeAgent, month, uuid.Nil())  // active, not expiring soon
	issue(limes.PrincipalTypeAgent, hour, uuid.Nil())           // active AND expiring within 24h
	issue(limes.PrincipalTypeAgent, lapsed, uuid.Nil())         // expired, never revoked
	revoke(issue(limes.PrincipalTypeAgent, month, uuid.Nil()))  // revoked while still valid
	revoke(issue(limes.PrincipalTypeAgent, lapsed, uuid.Nil())) // revoked AND expired — revocation wins
	issue(limes.PrincipalTypeService, month, live)              // active, delegated from `live`

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
			tenantID, "tok-"+uuid.New().String()[:8], uuid.New().String()[:8], expires, lastUsed, revoked)
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
		bc.ByPrincipalKind[string(limes.PrincipalTypeAgent)], c.ByPrincipalKind[string(limes.PrincipalTypeAgent)], 2)
	assertDelta(t, "capabilities.by_kind[service]",
		bc.ByPrincipalKind[string(limes.PrincipalTypeService)], c.ByPrincipalKind[string(limes.PrincipalTypeService)], 1)

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
