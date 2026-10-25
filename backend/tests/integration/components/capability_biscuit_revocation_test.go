//go:build integration

package components

import (
	"context"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/jwks"
	"github.com/oleg-tkachuk/limes/pgstore"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
)

// biscuitIssuerName is the issuer the Biscuit fixtures mint as.
const biscuitIssuerName = "paladin-biscuit-test"

// biscuitAudience is the audience the Biscuit fixtures' capabilities carry.
const biscuitAudience = "data"

// biscuitFixture mints capabilities into the relational store and seals them
// as Biscuits, so a test revokes copies the verifier genuinely read: only
// Verifier.BiscuitCopy names one.
type biscuitFixture struct {
	store    *pgstore.Store
	issuer   *limes.Issuer
	verifier *limes.Verifier
	clock    *fakeClock
}

// newBiscuitFixture records into pool's database through admin, on a clock
// the test moves.
func newBiscuitFixture(t *testing.T, admin *pgxpool.Pool) *biscuitFixture {
	t.Helper()
	clock := newFakeClock()
	store, err := capstore.New(admin, capstore.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	key, err := limes.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := limes.NewSigner(limes.SignerConfig{KeyID: key.KeyID, Key: key.Private})
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := limes.NewIssuer(limes.IssuerConfig{Signer: signer, Store: store, IssuerName: biscuitIssuerName, Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := jwks.NewStatic(jwks.StaticConfig{Keys: map[string]ed25519.PublicKey{key.KeyID: key.Public()}})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := limes.NewVerifier(limes.VerifierConfig{
		Keys: keys, Revocations: store, TrustedIssuers: []string{biscuitIssuerName}, Now: clock.Now,
		Biscuit: &limes.BiscuitConfig{Revocations: store, MeterCopies: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &biscuitFixture{store: store, issuer: issuer, verifier: verifier, clock: clock}
}

// seal mints a capability of tenant, verifies it and seals it as a Biscuit,
// returning the Biscuit's token and the capability.
func (f *biscuitFixture) seal(t *testing.T, tenant uuid.UUID, subject string) (string, limes.Capability) {
	t.Helper()
	ctx := context.Background()
	principal := limes.Principal{Type: limes.PrincipalTypeAgent, TenantID: tenant, Subject: subject}
	res, err := f.issuer.Issue(ctx, limes.IssueRequest{
		Subject: principal, IssuedBy: seedIssuer, Audience: []string{biscuitAudience},
		Caveats: limes.Caveats{
			// A Biscuit is sealed only for a capability that may be shared,
			// and a copy with limits of its own needs a quota to be counted.
			Ops:      []limes.Op{limes.OpGet, limes.OpList, limes.OpShare},
			Requests: limes.RequestCeiling{Unlimited: true}, Budget: limes.BudgetCeiling{Unlimited: true, UnitCode: testUnit},
			Copies: limes.CopyQuota{Unlimited: true},
		},
		TTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	verified, err := f.verifier.Verify(ctx, limes.VerifyRequest{Token: res.Token, Audience: biscuitAudience})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	b, err := f.issuer.Biscuit(ctx, limes.BiscuitRequest{Capability: verified})
	if err != nil {
		t.Fatalf("biscuit: %v", err)
	}
	return b.Token, res.Capability
}

// attenuate narrows token to ops, a copy with a revocation id of its own.
func attenuate(t *testing.T, token string, ops ...limes.Op) string {
	t.Helper()
	out, err := limes.Attenuate(token, limes.Attenuation{Ops: ops})
	if err != nil {
		t.Fatalf("attenuate: %v", err)
	}
	return out
}

// copyOf names the copy token is, as the verifier reads it.
func (f *biscuitFixture) copyOf(t *testing.T, token string) limes.BiscuitCopy {
	t.Helper()
	c, err := f.verifier.BiscuitCopy(context.Background(), token)
	if err != nil {
		t.Fatalf("biscuit copy: %v", err)
	}
	return c
}

// A revoked copy is seen by the verifier's connection, which has no tenant on
// it: the same trap IsRevoked guards against (TestCapabilityIsRevokedBeforeTheTenantIsKnown).
func TestBiscuitCopyRevokedBeforeTheTenantIsKnown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	f := newBiscuitFixture(t, admin)
	sealed, c := f.seal(t, tenant, "agent:copies")
	revokedToken := attenuate(t, sealed, limes.OpGet, limes.OpList)
	revoked := f.copyOf(t, revokedToken)
	fromIt := f.copyOf(t, attenuate(t, revokedToken, limes.OpGet))
	sibling := f.copyOf(t, attenuate(t, sealed, limes.OpList))
	for i := range 2 { // idempotent: the second keeps the first entry, and says so
		res, err := f.store.RevokeBiscuit(ctx, limes.RevokeBiscuitRequest{
			Copy: revoked, Note: "leak", Actor: "user:ops",
		})
		if err != nil {
			t.Fatalf("revoke copy: %v", err)
		}
		if res.AlreadyRevoked != (i > 0) || res.Revocation.Note != "leak" {
			t.Errorf("revoke %d: %+v", i, res)
		}
	}

	verifier := newCapStore(t, rlsPool(t, ctx, admin))
	for name, tc := range map[string]struct {
		ids  [][]byte
		want bool
	}{
		"the revoked copy":          {[][]byte{revoked.RevocationID()}, true},
		"a copy attenuated from it": {[][]byte{revoked.RevocationID(), fromIt.RevocationID()}, true},
		"a copy it was never in":    {[][]byte{sibling.RevocationID()}, false},
	} {
		got, err := verifier.IsBiscuitRevoked(ctx, tc.ids)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != tc.want {
			t.Errorf("%s: revoked = %v, want %v", name, got, tc.want)
		}
	}
	if revokedCap, err := verifier.IsRevoked(ctx, c.ID); err != nil || revokedCap {
		t.Errorf("revoking a copy revoked the capability: %v, %v", revokedCap, err)
	}
	got, err := f.store.GetBiscuitRevocation(ctx, revoked)
	if err != nil || got.CapabilityID != c.ID || got.Note != "leak" {
		t.Errorf("revocation read back = %+v, %v", got, err)
	}
}

// Tenant A cannot list a copy of tenant B's capability, on the runtime's
// NOBYPASSRLS connection: the scope from A's principal hides B's capability,
// so the copy's revocation finds nothing to list under.
func TestBiscuitCopyRevokeIsTenantScoped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenantA, _ := mkTenant(t, ctx, admin, "shared")
	tenantB, _ := mkTenant(t, ctx, admin, "shared")
	f := newBiscuitFixture(t, admin)
	sealed, victim := f.seal(t, tenantB, "agent:victim")
	hostile := f.copyOf(t, attenuate(t, sealed, limes.OpGet))

	scoped := newCapStore(t, rlsPool(t, ctx, admin))
	ctxA := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenantA})
	_, err := scoped.RevokeBiscuit(ctxA, limes.RevokeBiscuitRequest{Copy: hostile, Note: "hostile", Actor: "user:attacker"})
	if !errors.Is(err, limes.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	var rows int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM limes.biscuit_revocations WHERE capability_id = $1`, victim.ID,
	).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 0 {
		t.Errorf("tenant A listed a copy of tenant B's capability")
	}

	// The owning tenant can.
	ctxB := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenantB})
	if _, err := scoped.RevokeBiscuit(ctxB, limes.RevokeBiscuitRequest{Copy: hostile}); err != nil {
		t.Errorf("owning tenant: %v", err)
	}
}

// notifyDeadline bounds how long a revocation may take to clear another
// replica's cache by notification.
const notifyDeadline = 5 * time.Second

// notifyPoll is how often the notification test reads the cache.
const notifyPoll = 20 * time.Millisecond

// A copy revoked on one replica clears the others' caches by notification,
// on the channel capability revocations already use.
func TestBiscuitCopyRevocationNotifies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	f := newBiscuitFixture(t, admin)
	sealed, _ := f.seal(t, tenant, "agent:notify")
	copied := f.copyOf(t, attenuate(t, sealed, limes.OpGet))

	replica := newCapStore(t, rlsPool(t, ctx, admin))
	cache, err := limes.NewRevocationCache(limes.RevocationCacheConfig{Capabilities: replica, Biscuits: replica, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	watchCtx, stop := context.WithCancel(ctx)
	t.Cleanup(stop)
	watchRevocations(t, watchCtx, admin, cache)
	ids := [][]byte{copied.RevocationID()}
	if r, err := cache.IsBiscuitRevoked(ctx, ids); err != nil || r {
		t.Fatalf("before revoke: revoked=%v err=%v", r, err)
	}

	if _, err := f.store.RevokeBiscuit(ctx, limes.RevokeBiscuitRequest{Copy: copied, Note: "test", Actor: "user:ops"}); err != nil {
		t.Fatalf("revoke copy: %v", err)
	}

	deadline := time.Now().Add(notifyDeadline)
	for {
		r, err := cache.IsBiscuitRevoked(ctx, ids)
		if err != nil {
			t.Fatalf("IsBiscuitRevoked: %v", err)
		}
		if r {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the cached 'live' answer survived the copy's revocation for %s; the notification never cleared the cache", notifyDeadline)
		}
		time.Sleep(notifyPoll)
	}
}
