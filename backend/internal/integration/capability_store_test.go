//go:build integration

// The capability Postgres store is the record of who may do what, and it had
// no test against a real schema. Its SQL is hand-written (the package comment
// says so deliberately), which means nothing but a live database can tell it
// whether the columns still line up.
//
// Two behaviours here are load-bearing beyond round-tripping:
//
//   - Revoke must be scoped to the caller's tenant. capability_revocations
//     carries no tenant_id of its own, so isolation has to come from the
//     capability the revocation points at.
//   - ListByPrincipal's cursor must page a delegation tree without repeating
//     or skipping, because the console pages it.
package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/capability"
)

func newCapStore(t *testing.T, pool *pgxpool.Pool) *capstore.Store {
	t.Helper()
	s, err := capstore.New(pool)
	if err != nil {
		t.Fatalf("new capability store: %v", err)
	}
	return s
}

// mkCap builds a capability with every optional field populated so a
// round-trip that drops one is visible.
func mkCap(tenant uuid.UUID, subject string, expires time.Time) capability.Capability {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return capability.Capability{
		ID:     uuid.New(),
		Issuer: "paladin-core/test",
		Subject: capability.Principal{
			Type:     capability.PrincipalUser,
			TenantID: tenant,
			Subject:  subject,
		},
		Audience: []string{"data", "mcp"},
		Caveats: capability.Caveats{
			Ops:                    []capability.Op{capability.OpGet, capability.OpList},
			ResourceURIs:           []string{"paladin://docs/"},
			MaxRequests:            42,
			MaxBudgetAmount:        12.5,
			UnitCode:               "USD",
			AllowTaintedRead:       true,
			IdempotencyKeyRequired: true,
			SourceIPCIDR:           []string{"10.0.0.0/8"},
		},
		IssuedAt:   now,
		NotBefore:  now.Add(-time.Minute),
		ExpiresAt:  expires.UTC().Truncate(time.Microsecond),
		Generation: 7,
	}
}

var seedIssuer = capability.Principal{Type: capability.PrincipalService, Subject: "svc:seed"}

// TestCapabilityRoundTrip pins Insert → Get across every column, including
// the two JSON-encoded ones the package chose hand-written SQL to handle.
func TestCapabilityRoundTrip(t *testing.T) {
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	store := newCapStore(t, admin)

	want := mkCap(tenant, "user:alice", time.Now().Add(time.Hour))
	if err := store.Insert(ctx, want, seedIssuer); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := store.Get(ctx, want.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != want.ID || got.Issuer != want.Issuer || got.Generation != want.Generation {
		t.Errorf("scalars: got %+v", got)
	}
	if got.Subject.TenantID != tenant || got.Subject.Subject != want.Subject.Subject ||
		got.Subject.Type != want.Subject.Type {
		t.Errorf("principal: got %+v want %+v", got.Subject, want.Subject)
	}
	assertStrings(t, "audience", got.Audience, want.Audience)
	if len(got.Caveats.Ops) != 2 || got.Caveats.Ops[0] != capability.OpGet {
		t.Errorf("caveats.ops: got %v", got.Caveats.Ops)
	}
	if got.Caveats.MaxRequests != want.Caveats.MaxRequests ||
		got.Caveats.MaxBudgetAmount != want.Caveats.MaxBudgetAmount ||
		got.Caveats.UnitCode != want.Caveats.UnitCode {
		t.Errorf("caveats budget: got %+v", got.Caveats)
	}
	if !got.Caveats.AllowTaintedRead || !got.Caveats.IdempotencyKeyRequired {
		t.Errorf("caveats booleans lost: %+v", got.Caveats)
	}
	assertStrings(t, "caveats.source_ip_cidr", got.Caveats.SourceIPCIDR, want.Caveats.SourceIPCIDR)
	if !got.NotBefore.Equal(want.NotBefore) || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("times: nbf %v exp %v", got.NotBefore, got.ExpiresAt)
	}

	if _, err := store.Get(ctx, uuid.New()); !errors.Is(err, capability.ErrNotFound) {
		t.Errorf("missing capability: want ErrNotFound, got %v", err)
	}
}

// TestCapabilityRevokeCascade pins that revoking a parent denies the whole
// delegation subtree in one transaction, and that the non-cascade path leaves
// children usable. A delegation tree that survives its root's revocation is
// the failure this guards.
func TestCapabilityRevokeCascade(t *testing.T) {
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	store := newCapStore(t, admin)

	// root → child → grandchild, plus an unrelated sibling tree.
	root := mkCap(tenant, "user:root", time.Now().Add(time.Hour))
	child := mkCap(tenant, "user:child", time.Now().Add(time.Hour))
	child.ParentID = root.ID
	grandchild := mkCap(tenant, "user:grandchild", time.Now().Add(time.Hour))
	grandchild.ParentID = child.ID
	unrelated := mkCap(tenant, "user:unrelated", time.Now().Add(time.Hour))

	for _, c := range []capability.Capability{root, child, grandchild, unrelated} {
		if err := store.Insert(ctx, c, seedIssuer); err != nil {
			t.Fatalf("insert %s: %v", c.Subject.Subject, err)
		}
	}

	revoked := func(id uuid.UUID) bool {
		t.Helper()
		got, err := store.IsRevoked(ctx, id)
		if err != nil {
			t.Fatalf("is_revoked: %v", err)
		}
		return got
	}

	for _, c := range []capability.Capability{root, child, grandchild, unrelated} {
		if revoked(c.ID) {
			t.Fatalf("%s revoked before any Revoke call", c.Subject.Subject)
		}
	}

	if err := store.Revoke(ctx, capability.RevokeArgs{
		ID: root.ID, Reason: "compromise", Actor: "user:ops", CascadeChildren: true,
	}); err != nil {
		t.Fatalf("cascade revoke: %v", err)
	}

	for _, c := range []capability.Capability{root, child, grandchild} {
		if !revoked(c.ID) {
			t.Errorf("%s survived the cascade", c.Subject.Subject)
		}
	}
	if revoked(unrelated.ID) {
		t.Error("cascade reached an unrelated tree")
	}

	// Re-revoking is a no-op, not a primary-key violation: the purger and
	// the admin RPC can both call it.
	if err := store.Revoke(ctx, capability.RevokeArgs{ID: root.ID, Reason: "again", Actor: "user:ops"}); err != nil {
		t.Errorf("re-revoke should be idempotent: %v", err)
	}

	if err := store.Revoke(ctx, capability.RevokeArgs{}); err == nil {
		t.Error("want error for nil revoke ID")
	}
}

// TestCapabilityRevokeIsTenantScoped is the isolation question that matters
// most for this table: capability_revocations has no tenant_id column, so the
// only thing standing between tenant A and a denial-of-service on tenant B's
// capabilities is that the revocation's foreign key points at a row A cannot
// see. Run on a NOBYPASSRLS pool, which is how the runtime connects.
func TestCapabilityRevokeIsTenantScoped(t *testing.T) {
	ctx := context.Background()
	admin := startPostgres(t)
	tenantA, _ := mkTenant(t, ctx, admin, "shared")
	tenantB, _ := mkTenant(t, ctx, admin, "shared")

	victim := mkCap(tenantB, "user:victim", time.Now().Add(time.Hour))
	if err := newCapStore(t, admin).Insert(ctx, victim, seedIssuer); err != nil {
		t.Fatalf("seed: %v", err)
	}

	pool := rlsPool(t, ctx, admin)
	scoped := newCapStore(t, pool)
	ctxA := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenantA})

	// A must not be able to read B's capability at all.
	if _, err := scoped.Get(ctxA, victim.ID); !errors.Is(err, capability.ErrNotFound) {
		t.Errorf("tenant A read B's capability: %v", err)
	}

	// And must not be able to revoke it. Either an error or a silent no-op is
	// an acceptable outcome; a recorded revocation is not.
	revokeErr := scoped.Revoke(ctxA, capability.RevokeArgs{
		ID: victim.ID, Reason: "hostile", Actor: "user:attacker",
	})

	// Read the result back with the admin pool, which sees everything.
	var revocations int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM capability_revocations WHERE id = $1`, victim.ID,
	).Scan(&revocations); err != nil {
		t.Fatalf("count revocations: %v", err)
	}
	if revocations != 0 {
		t.Errorf("tenant A revoked tenant B's capability (revoke returned %v); "+
			"capability_revocations has no tenant_id and no RLS policy", revokeErr)
	}
}

// TestCapabilityListByPrincipal pins the filters and the cursor. The default
// view hides expired and revoked capabilities, which is what makes the
// console's "active credentials" count trustworthy.
func TestCapabilityListByPrincipal(t *testing.T) {
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	other, _ := mkTenant(t, ctx, admin, "shared")
	store := newCapStore(t, admin)

	const subject = "user:paged"
	active := make([]uuid.UUID, 0, 5)
	for i := 0; i < 5; i++ {
		c := mkCap(tenant, subject, time.Now().Add(time.Hour))
		if err := store.Insert(ctx, c, seedIssuer); err != nil {
			t.Fatalf("insert active %d: %v", i, err)
		}
		active = append(active, c.ID)
	}
	expired := mkCap(tenant, subject, time.Now().Add(-time.Hour))
	revoked := mkCap(tenant, subject, time.Now().Add(time.Hour))
	otherSubject := mkCap(tenant, "user:someone-else", time.Now().Add(time.Hour))
	otherTenant := mkCap(other, subject, time.Now().Add(time.Hour))
	for _, c := range []capability.Capability{expired, revoked, otherSubject, otherTenant} {
		if err := store.Insert(ctx, c, seedIssuer); err != nil {
			t.Fatalf("insert %s: %v", c.ID, err)
		}
	}
	if err := store.Revoke(ctx, capability.RevokeArgs{ID: revoked.ID, Reason: "r", Actor: "a"}); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	list := func(args capability.ListByPrincipalArgs) ([]capability.Capability, string) {
		t.Helper()
		args.TenantID = tenant
		args.PrincipalT = capability.PrincipalUser
		args.Subject = subject
		got, next, err := store.ListByPrincipal(ctx, args)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		return got, next
	}

	t.Run("defaults hide expired, revoked, other subjects and other tenants", func(t *testing.T) {
		got, next := list(capability.ListByPrincipalArgs{})
		if next != "" {
			t.Errorf("unexpected cursor %q", next)
		}
		if len(got) != len(active) {
			t.Fatalf("got %d capabilities, want %d", len(got), len(active))
		}
	})

	t.Run("include_expired widens by exactly one", func(t *testing.T) {
		got, _ := list(capability.ListByPrincipalArgs{IncludeExpired: true})
		if len(got) != len(active)+1 {
			t.Errorf("got %d, want %d", len(got), len(active)+1)
		}
	})

	t.Run("include_revoked widens by exactly one", func(t *testing.T) {
		got, _ := list(capability.ListByPrincipalArgs{IncludeRevoked: true})
		if len(got) != len(active)+1 {
			t.Errorf("got %d, want %d", len(got), len(active)+1)
		}
	})

	t.Run("cursor pages without repeats or gaps", func(t *testing.T) {
		seen := map[uuid.UUID]int{}
		cursor := ""
		for page := 0; ; page++ {
			if page > 10 {
				t.Fatal("pagination did not terminate")
			}
			got, next := list(capability.ListByPrincipalArgs{Limit: 2, Cursor: cursor})
			for _, c := range got {
				seen[c.ID]++
			}
			if next == "" {
				break
			}
			cursor = next
		}
		if len(seen) != len(active) {
			t.Fatalf("paged %d distinct capabilities, want %d", len(seen), len(active))
		}
		for id, n := range seen {
			if n != 1 {
				t.Errorf("%s returned %d times", id, n)
			}
		}
	})

	t.Run("tenant_id is required", func(t *testing.T) {
		if _, _, err := store.ListByPrincipal(ctx, capability.ListByPrincipalArgs{}); err == nil {
			t.Error("want error for nil tenant_id")
		}
	})
}

// TestCapabilityPurgeExpired pins the grace boundary on the denylist. Purging
// too eagerly un-revokes a capability that has not yet expired, which is a
// security regression rather than a cosmetic one.
func TestCapabilityPurgeExpired(t *testing.T) {
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	store := newCapStore(t, admin)

	longExpired := mkCap(tenant, "user:long", time.Now().Add(-48*time.Hour))
	justExpired := mkCap(tenant, "user:just", time.Now().Add(-time.Minute))
	live := mkCap(tenant, "user:live", time.Now().Add(time.Hour))
	for _, c := range []capability.Capability{longExpired, justExpired, live} {
		if err := store.Insert(ctx, c, seedIssuer); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if err := store.Revoke(ctx, capability.RevokeArgs{ID: c.ID, Reason: "r", Actor: "a"}); err != nil {
			t.Fatalf("revoke: %v", err)
		}
	}

	n, err := store.PurgeExpired(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged %d revocations, want 1", n)
	}

	for _, tc := range []struct {
		cap  capability.Capability
		want bool
	}{
		{longExpired, false}, // denylist entry dropped; the capability is expired anyway
		{justExpired, true},  // inside grace — still denied
		{live, true},         // not expired — must still be denied
	} {
		got, err := store.IsRevoked(ctx, tc.cap.ID)
		if err != nil {
			t.Fatalf("is_revoked: %v", err)
		}
		if got != tc.want {
			t.Errorf("%s: revoked=%v want %v", tc.cap.Subject.Subject, got, tc.want)
		}
	}
}

// TestCapabilityInsertCrossTenant pins the reason Insert opens its own
// transaction and sets the tenant GUC: a platform admin issuing a capability
// for another tenant carries the platform tenant on its JWT, and without the
// SET LOCAL the row's own tenant_id would trip the RLS WITH CHECK.
func TestCapabilityInsertCrossTenant(t *testing.T) {
	ctx := context.Background()
	admin := startPostgres(t)
	platform, _ := mkTenant(t, ctx, admin, "shared")
	target, _ := mkTenant(t, ctx, admin, "shared")

	pool := rlsPool(t, ctx, admin)
	store := newCapStore(t, pool)
	ctxPlatform := auth.WithPrincipal(ctx, &auth.Principal{TenantID: platform})

	c := mkCap(target, "user:target", time.Now().Add(time.Hour))
	if err := store.Insert(ctxPlatform, c, seedIssuer); err != nil {
		t.Fatalf("platform admin could not issue for another tenant: %v", err)
	}

	var count int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM capability_records WHERE id = $1 AND tenant_id = $2`, c.ID, target,
	).Scan(&count); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if count != 1 {
		t.Fatalf("capability not stored against the target tenant (found %d)", count)
	}

	// The GUC swap must be transaction-local: the same pooled connection,
	// used again, must be back on the platform tenant's scope.
	if _, err := store.Get(ctxPlatform, c.ID); !errors.Is(err, capability.ErrNotFound) {
		t.Errorf("tenant GUC leaked past the insert transaction: %v", err)
	}
}

// TestPurgeRunsWithoutARequestPrincipal pins the failure mode that background
// jobs on an RLS-scoped pool keep rediscovering: the purger runs on a timer,
// so there is no principal, so there is no session tenant, so every
// tenant-scoped statement matches nothing and the job logs success forever
// while the table grows.
//
// The assertion is on both halves — without the cross-tenant flag the purge is
// inert, with it the purge works — because a test that only checked the second
// would pass just as well if RLS were switched off entirely.
func TestPurgeRunsWithoutARequestPrincipal(t *testing.T) {
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")

	seed := newCapStore(t, admin)
	expired := mkCap(tenant, "user:expired", time.Now().Add(-48*time.Hour))
	if err := seed.Insert(ctx, expired, seedIssuer); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := seed.Revoke(ctx, capability.RevokeArgs{ID: expired.ID, Reason: "r", Actor: "a"}); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	purger := newCapStore(t, rlsPool(t, ctx, admin))

	// A bare context is what a ticker hands its job.
	n, err := purger.PurgeExpired(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("purge without principal: %v", err)
	}
	if n != 0 {
		t.Fatalf("purged %d rows with no session tenant — RLS is not engaging on this pool, so this test proves nothing", n)
	}

	n, err = purger.PurgeExpired(auth.WithCrossTenantRead(ctx), 24*time.Hour)
	if err != nil {
		t.Fatalf("purge with cross-tenant read: %v", err)
	}
	if n != 1 {
		t.Errorf("purged %d rows with the cross-tenant flag, want 1 — the background purger cannot do its job", n)
	}
}
