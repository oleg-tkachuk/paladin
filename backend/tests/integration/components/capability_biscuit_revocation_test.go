//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/capability"
)

// A revoked copy is seen by the verifier's connection, which has no tenant on
// it: the same trap IsRevoked fell into before it read cross-tenant.
func TestBiscuitCopyRevokedBeforeTheTenantIsKnown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	c := mkCap(tenant, "agent:copies", time.Now().Add(time.Hour))
	seed := newCapStore(t, admin)
	if err := seed.Insert(ctx, c, seedIssuer); err != nil {
		t.Fatalf("insert: %v", err)
	}
	revoked, other := []byte("revoked-copy"), []byte("other-copy")
	for range 2 { // idempotent
		if err := seed.RevokeBiscuit(ctx, capability.RevokeBiscuitRequest{
			CapabilityID: c.ID, RevocationID: revoked, Reason: "leak", Actor: "user:ops",
		}); err != nil {
			t.Fatalf("revoke copy: %v", err)
		}
	}

	verifier := newCapStore(t, rlsPool(t, ctx, admin))
	for name, tc := range map[string]struct {
		ids  [][]byte
		want bool
	}{
		"the revoked copy":          {[][]byte{revoked}, true},
		"a copy attenuated from it": {[][]byte{revoked, other}, true},
		"a copy it was never in":    {[][]byte{other}, false},
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
}

// Tenant A cannot list a copy of tenant B's capability: the copy's row is
// isolated through the capability it points at, on the runtime's NOBYPASSRLS
// connection.
func TestBiscuitCopyRevokeIsTenantScoped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenantA, _ := mkTenant(t, ctx, admin, "shared")
	tenantB, _ := mkTenant(t, ctx, admin, "shared")
	victim := mkCap(tenantB, "agent:victim", time.Now().Add(time.Hour))
	if err := newCapStore(t, admin).Insert(ctx, victim, seedIssuer); err != nil {
		t.Fatalf("seed: %v", err)
	}

	scoped := newCapStore(t, rlsPool(t, ctx, admin))
	ctxA := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenantA})
	err := scoped.RevokeBiscuit(ctxA, capability.RevokeBiscuitRequest{
		CapabilityID: victim.ID, RevocationID: []byte("hostile"), Reason: "hostile", Actor: "user:attacker",
	})
	if !errors.Is(err, capability.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	var rows int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM capability_biscuit_revocations WHERE capability_id = $1`, victim.ID,
	).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 0 {
		t.Errorf("tenant A listed a copy of tenant B's capability")
	}

	// The owning tenant can.
	ctxB := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenantB})
	if err := scoped.RevokeBiscuit(ctxB, capability.RevokeBiscuitRequest{
		CapabilityID: victim.ID, RevocationID: []byte("own"),
	}); err != nil {
		t.Errorf("owning tenant: %v", err)
	}
}

// Revoked copies are purged on the capability's grace, like its own
// revocation: never while the capability could still verify.
func TestBiscuitCopiesPurgedWithTheirCapability(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	store := newCapStore(t, admin)
	longExpired := mkCap(tenant, "agent:long", time.Now().Add(-48*time.Hour))
	live := mkCap(tenant, "agent:live", time.Now().Add(time.Hour))
	for _, c := range []capability.Capability{longExpired, live} {
		if err := store.Insert(ctx, c, seedIssuer); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if err := store.RevokeBiscuit(ctx, capability.RevokeBiscuitRequest{
			CapabilityID: c.ID, RevocationID: []byte(c.Subject.Subject),
		}); err != nil {
			t.Fatalf("revoke copy: %v", err)
		}
	}

	n, err := store.PurgeExpired(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged %d rows, want 1", n)
	}
	for _, tc := range []struct {
		cap  capability.Capability
		want bool
	}{
		{longExpired, false},
		{live, true},
	} {
		got, err := store.IsBiscuitRevoked(ctx, [][]byte{[]byte(tc.cap.Subject.Subject)})
		if err != nil {
			t.Fatalf("is_biscuit_revoked: %v", err)
		}
		if got != tc.want {
			t.Errorf("%s: revoked = %v, want %v", tc.cap.Subject.Subject, got, tc.want)
		}
	}
}

// A copy revoked on one replica clears the others' caches by notification,
// on the channel capability revocations already use.
func TestBiscuitCopyRevocationNotifies(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)

	replica := newCapStore(t, rlsPool(t, ctx, f.pool))
	cache := capability.NewCachedBiscuitRevocationChecker(replica, time.Hour)
	watchCtx, stop := context.WithCancel(ctx)
	t.Cleanup(stop)
	if err := capstore.NewRevocationWatcher(f.pool, cache.Clear).Start(watchCtx); err != nil {
		t.Fatalf("start watcher: %v", err)
	}
	ids := [][]byte{[]byte("copy")}
	if r, err := cache.IsBiscuitRevoked(ctx, ids); err != nil || r {
		t.Fatalf("before revoke: revoked=%v err=%v", r, err)
	}

	if err := f.records.RevokeBiscuit(ctx, capability.RevokeBiscuitRequest{
		CapabilityID: f.root, RevocationID: ids[0], Reason: "test", Actor: "user:ops",
	}); err != nil {
		t.Fatalf("revoke copy: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		r, err := cache.IsBiscuitRevoked(ctx, ids)
		if err != nil {
			t.Fatalf("IsBiscuitRevoked: %v", err)
		}
		if r {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the cached 'live' answer survived the copy's revocation for 5s; the notification never cleared the cache")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
