//go:build integration

// The capability store is limes/pgstore's, contract-tested by limes and here
// by capability_store_contract_test.go. What these tests pin is Paladin's
// side of it, on the application role as the server connects:
//
//   - the scope: capability/postgres.Scope maps a request's tenant, acting
//     tenant or platform-wide flag onto the limes schema's row-level
//     security, so one tenant cannot read or revoke another's capability;
//   - the verifier's revocation check, made before any tenant is known;
//   - the purger, which carries no principal and widens itself explicitly;
//   - the tenant check, which refuses a tenant Paladin does not hold.
package components

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/pgstore"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
)

func newCapStore(t *testing.T, pool *pgxpool.Pool) *pgstore.Store {
	t.Helper()
	s, err := capstore.New(pool)
	if err != nil {
		t.Fatalf("new capability store: %v", err)
	}
	return s
}

// mkCap builds a capability with every optional field populated so a
// round-trip that drops one is visible.
func mkCap(tenant uuid.UUID, subject string, expires time.Time) limes.Capability {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return limes.Capability{
		ID:     uuid.New(),
		Issuer: "paladin-core/test",
		Subject: limes.Principal{
			Type:     limes.PrincipalTypeUser,
			TenantID: tenant,
			Subject:  subject,
		},
		Audience: []string{"data", "mcp"},
		Caveats: limes.Caveats{
			Ops:                    []limes.Op{limes.OpGet, limes.OpList},
			ResourceURIs:           []string{"paladin://docs/"},
			Requests:               limes.RequestCeiling{Max: 42},
			Budget:                 limes.BudgetCeiling{Max: limes.MustParseAmount("12.5"), UnitCode: "USD"},
			AllowTaintedRead:       true,
			IdempotencyKeyRequired: true,
			SourceIPCIDR:           []string{"10.0.0.0/8"},
		},
		IssuedAt:  now,
		NotBefore: now.Add(-time.Minute),
		ExpiresAt: expires.UTC().Truncate(time.Microsecond),
	}
}

var seedIssuer = limes.Principal{Type: limes.PrincipalTypeService, Subject: "svc:seed"}

// purgeRetention is how long past its expiry the purge tests keep a record.
const purgeRetention = 24 * time.Hour

// getCapability reads back the capability a record holds.
func getCapability(ctx context.Context, s *pgstore.Store, id uuid.UUID) (limes.Capability, error) {
	rec, err := s.GetRecord(ctx, id)
	return rec.Capability, err
}

// TestCapabilityRevokeIsTenantScoped pins that tenant A can neither read nor
// revoke tenant B's capability: a revocation is a denial of service on the
// capability it names, so its isolation is the scope Paladin puts on the
// transaction, from the caller's own tenant.
func TestCapabilityRevokeIsTenantScoped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenantA, _ := mkTenant(t, ctx, admin, "shared")
	tenantB, _ := mkTenant(t, ctx, admin, "shared")

	victim := mkCap(tenantB, "user:victim", time.Now().Add(time.Hour))
	if err := newCapStore(t, admin).Insert(ctx, limes.InsertRequest{Capability: victim, IssuedBy: seedIssuer}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	pool := rlsPool(t, ctx, admin)
	scoped := newCapStore(t, pool)
	ctxA := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenantA})

	if _, err := getCapability(ctxA, scoped, victim.ID); !errors.Is(err, limes.ErrNotFound) {
		t.Errorf("tenant A read B's capability: %v", err)
	}
	if _, err := scoped.Revoke(ctxA, limes.RevokeRequest{
		CapabilityID: victim.ID, Note: "hostile", Actor: "user:attacker",
	}); !errors.Is(err, limes.ErrNotFound) {
		t.Errorf("tenant A revoking B's capability: err = %v, want ErrNotFound", err)
	}

	// Read the result back with the admin pool, which sees everything.
	var revocations int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM limes.revocations WHERE capability_id = $1`, victim.ID,
	).Scan(&revocations); err != nil {
		t.Fatalf("count revocations: %v", err)
	}
	if revocations != 0 {
		t.Error("tenant A revoked tenant B's capability")
	}

	// The owning tenant can.
	ctxB := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenantB})
	if _, err := scoped.Revoke(ctxB, limes.RevokeRequest{CapabilityID: victim.ID, Note: "own", Actor: "user:ops"}); err != nil {
		t.Errorf("owning tenant: %v", err)
	}
}

// TestCapabilityIsRevokedBeforeTheTenantIsKnown pins the verifier's view of
// the denylist. Verify runs before anything has put a tenant on the context —
// the tenant is inside the token it is still checking — so the transaction
// scope sees no row. A revocation check bound by that scope answers false for
// every capability, and a revoked one keeps working on the data plane, where
// it is the whole credential.
func TestCapabilityIsRevokedBeforeTheTenantIsKnown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")

	revokedCap := mkCap(tenant, "agent:revoked", time.Now().Add(time.Hour))
	liveCap := mkCap(tenant, "agent:live", time.Now().Add(time.Hour))
	seed := newCapStore(t, admin)
	for _, c := range []limes.Capability{revokedCap, liveCap} {
		if err := seed.Insert(ctx, limes.InsertRequest{Capability: c, IssuedBy: seedIssuer}); err != nil {
			t.Fatalf("seed %s: %v", c.Subject.Subject, err)
		}
	}
	if _, err := seed.Revoke(ctx, limes.RevokeRequest{
		CapabilityID: revokedCap.ID, Note: "compromise", Actor: "user:ops",
	}); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	// The runtime's connection, with no principal on the context.
	verifier := newCapStore(t, rlsPool(t, ctx, admin))
	for _, tc := range []struct {
		cap  limes.Capability
		want bool
	}{
		{revokedCap, true},
		{liveCap, false},
	} {
		got, err := verifier.IsRevoked(ctx, tc.cap.ID)
		if err != nil {
			t.Fatalf("is_revoked %s: %v", tc.cap.Subject.Subject, err)
		}
		if got != tc.want {
			t.Errorf("IsRevoked(%s) with no tenant on the context = %v, want %v",
				tc.cap.Subject.Subject, got, tc.want)
		}
	}
}

// TestCapabilityInsertCrossTenant pins how a platform admin issues for
// another tenant: its JWT carries the platform tenant, so the issuance handler
// acts on the target tenant (auth.WithActingTenant), and the scope follows it.
// Without the swap the row's own tenant_id trips the policy's WITH CHECK.
func TestCapabilityInsertCrossTenant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	platform, _ := mkTenant(t, ctx, admin, "shared")
	target, _ := mkTenant(t, ctx, admin, "shared")

	pool := rlsPool(t, ctx, admin)
	store := newCapStore(t, pool)
	ctxPlatform := auth.WithPrincipal(ctx, &auth.Principal{TenantID: platform})

	stored := func(id uuid.UUID) int {
		t.Helper()
		var n int
		if err := admin.QueryRow(ctx,
			`SELECT count(*) FROM limes.capabilities WHERE id = $1 AND tenant_id = $2`, id, target,
		).Scan(&n); err != nil {
			t.Fatalf("verify: %v", err)
		}
		return n
	}

	unswapped := mkCap(target, "user:target", time.Now().Add(time.Hour))
	if err := store.Insert(ctxPlatform, limes.InsertRequest{Capability: unswapped, IssuedBy: seedIssuer}); err == nil {
		t.Error("SECURITY: wrote a capability of the target tenant from the platform tenant's scope")
	}
	if n := stored(unswapped.ID); n != 0 {
		t.Errorf("the refused insert stored %d rows", n)
	}

	c := mkCap(target, "user:target", time.Now().Add(time.Hour))
	if err := store.Insert(auth.WithActingTenant(ctxPlatform, target), limes.InsertRequest{Capability: c, IssuedBy: seedIssuer}); err != nil {
		t.Fatalf("platform admin acting on the target could not issue for it: %v", err)
	}
	if n := stored(c.ID); n != 1 {
		t.Fatalf("capability not stored against the target tenant (found %d)", n)
	}

	// The scope is transaction-local: the same pooled connection, used
	// again, is back on the platform tenant's scope.
	if _, err := getCapability(ctxPlatform, store, c.ID); !errors.Is(err, limes.ErrNotFound) {
		t.Errorf("tenant scope leaked past the insert transaction: %v", err)
	}
}

// TestPurgeRunsWithoutARequestPrincipal pins the failure mode that background
// jobs on an RLS-scoped pool keep rediscovering: the purger runs on a timer,
// so there is no principal and no tenant, so every statement matches nothing
// and the job logs success forever while the table grows. The purger scopes
// its context to every tenant, to read and write (pgstore.WithAllTenants); a
// platform-wide read is refused, as it may write nothing.
//
// The assertion is on every half — the record is invisible and unpurged on
// a bare context, and the platform-wide purge removes it — because a test
// that only checked the last would pass just as well if RLS were off.
func TestPurgeRunsWithoutARequestPrincipal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")

	seed := newCapStore(t, admin)
	expired := mkCap(tenant, "user:expired", time.Now().Add(-2*purgeRetention))
	if err := seed.Insert(ctx, limes.InsertRequest{Capability: expired, IssuedBy: seedIssuer}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := seed.Revoke(ctx, limes.RevokeRequest{CapabilityID: expired.ID, Note: "r", Actor: "a"}); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	purger := newCapStore(t, rlsPool(t, ctx, admin))

	// A bare context is what a ticker hands its job.
	if _, err := purger.GetRecord(ctx, expired.ID); !errors.Is(err, limes.ErrNotFound) {
		t.Fatalf("the record reads back with no tenant (%v) — RLS is not engaging on this pool, so this test proves nothing", err)
	}
	res, err := purger.Purge(ctx, limes.PurgeRequest{ExpiredFor: purgeRetention})
	if err != nil {
		t.Fatalf("purge on a bare context: %v", err)
	}
	if res.Purged != 0 {
		t.Fatalf("purged %d records on a bare context — the scope is not engaging", res.Purged)
	}
	// A platform-wide read writes nothing: it is refused before it begins.
	if _, err := purger.Purge(auth.WithCrossTenantRead(ctx), limes.PurgeRequest{ExpiredFor: purgeRetention}); !errors.Is(err, limes.ErrScopeMismatch) {
		t.Fatalf("purge under a read-only scope: err = %v, want ErrScopeMismatch", err)
	}
	// Housekeeping, as the purger scopes it.
	res, err = purger.Purge(pgstore.WithAllTenants(ctx), limes.PurgeRequest{ExpiredFor: purgeRetention})
	if err != nil {
		t.Fatalf("platform-wide purge: %v", err)
	}
	if res.Purged != 1 {
		t.Errorf("purged %d records platform-wide, want 1 — the background purger cannot do its job", res.Purged)
	}
}

// An issuance for a tenant that does not exist reached the client as the
// driver's foreign-key error, and one for a tenant in the trash succeeded:
// the row exists, so the key admits it. Both are refused by name — as the
// application role, so the tenant lock runs with the privileges it has in
// production, and acting on the tenant, as the issuance handler does.
func TestInsertRefusesATenantItCannotIssueFor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	store := newCapStore(t, rlsPool(t, ctx, admin))

	live, _ := mkTenant(t, ctx, admin, "shared")
	trashed, _ := mkTenant(t, ctx, admin, "shared")
	mustExec(t, ctx, admin, `UPDATE tenants SET deleted_at = now() WHERE id = $1`, trashed)

	for name, tc := range map[string]struct {
		tenant uuid.UUID
		want   error
	}{
		"a live tenant":     {live, nil},
		"an unknown tenant": {uuid.New(), limes.ErrUnknownTenant},
		"a tenant in trash": {trashed, limes.ErrTenantDeleted},
	} {
		t.Run(name, func(t *testing.T) {
			err := store.Insert(auth.WithActingTenant(ctx, tc.tenant), limes.InsertRequest{
				Capability: mkCap(tc.tenant, "user:alice", time.Now().Add(time.Hour)), IssuedBy: seedIssuer,
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("insert: %v, want %v", err, tc.want)
			}
			if err != nil && strings.Contains(err.Error(), "SQLSTATE") {
				t.Errorf("the refusal carries the driver's error: %v", err)
			}
		})
	}
}
