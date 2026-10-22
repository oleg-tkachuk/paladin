//go:build integration

package components

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/platformstats"
)

// seedCapability puts a capability of tenant, issued to a principal of kind,
// on record through the capability store as issuance does, and returns its
// id. parent is uuid.Nil for a root capability.
func seedCapability(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant uuid.UUID, kind limes.PrincipalType, expires time.Time, parent uuid.UUID) uuid.UUID {
	t.Helper()
	c := mkCap(tenant, "sub", expires)
	c.Subject.Type = kind
	c.ParentID = parent
	if err := newCapStore(t, pool).Insert(ctx, limes.InsertRequest{Capability: c, IssuedBy: seedIssuer}); err != nil {
		t.Fatalf("seed capability: %v", err)
	}
	return c.ID
}

// seedRevocation revokes the capability id through the capability store.
func seedRevocation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	if _, err := newCapStore(t, pool).Revoke(ctx, limes.RevokeRequest{CapabilityID: id, Note: "fixture", Actor: "fixture-issuer"}); err != nil {
		t.Fatalf("seed revocation: %v", err)
	}
}

// TestCollectControlPlane proves the inventory aggregates that back the
// console's /stats page count the right things: the tenant active/trashed
// split, the layout split (active rows only), backend enable/drain flags,
// and the bucket provision-state / ownership breakdowns.
//
// The fixture seeds one tenant + one backend + one bucket that tenant owns +
// one object key; this test adds a second, deliberately-degenerate set on top
// and asserts on deltas so it stays correct if seedFixture grows.
func TestCollectControlPlane(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	base, err := platformstats.CollectControlPlane(ctx, pool)
	if err != nil {
		t.Fatalf("baseline census: %v", err)
	}
	f := seedFixture(t, ctx, pool)

	// A trashed, dedicated tenant with no default binding.
	trashed := uuid.New()
	hex := uuid.NewString()[:8]
	mustExec(t, ctx, pool,
		`INSERT INTO tenants (id, slug, display_name, storage_layout, deleted_at)
		 VALUES ($1, $2, $3, 'dedicated', now())`,
		trashed, "t-"+hex, "tn-"+hex)
	// A disabled + drained backend of a different kind.
	mustExec(t, ctx, pool,
		`INSERT INTO storage_backends (name, kind, enabled, read_only) VALUES ($1, 'aws-s3', false, true)`,
		"be-off-"+hex)
	// A tenant-owned bucket that never finished provisioning.
	mustExec(t, ctx, pool,
		`INSERT INTO buckets (backend_id, name, owner_tenant_id, provision_state, versioning_enabled)
		 SELECT sb.id, $2, $3, 'failed', true FROM storage_backends sb WHERE sb.name = $1`,
		"be-off-"+hex, "bkt-own-"+hex, f.tenantID)
	// That bucket granted to the trashed tenant: a shared bucket (ADR-0032).
	grantBucket(t, ctx, pool, trashed, "be-off-"+hex, "bkt-own-"+hex)

	got, err := platformstats.CollectControlPlane(ctx, pool)
	if err != nil {
		t.Fatalf("CollectControlPlane: %v", err)
	}

	// Tenants: fixture adds one active/shared, this test adds one
	// trashed/dedicated. Only the active one counts toward the layout
	// split, and only it counts as missing a default binding (the
	// fixture writes no tenant_default_bindings row).
	assertDelta(t, "tenants.total", base.Tenants.Total, got.Tenants.Total, 2)
	assertDelta(t, "tenants.active", base.Tenants.Active, got.Tenants.Active, 1)
	assertDelta(t, "tenants.trashed", base.Tenants.Trashed, got.Tenants.Trashed, 1)
	assertDelta(t, "tenants.shared", base.Tenants.SharedLayout, got.Tenants.SharedLayout, 1)
	assertDelta(t, "tenants.dedicated", base.Tenants.DedicatedLayout, got.Tenants.DedicatedLayout, 0)
	assertDelta(t, "tenants.unbound", base.Tenants.WithoutDefaultBinding, got.Tenants.WithoutDefaultBinding, 1)

	assertDelta(t, "backends.total", base.Backends.Total, got.Backends.Total, 2)
	assertDelta(t, "backends.enabled", base.Backends.Enabled, got.Backends.Enabled, 1)
	assertDelta(t, "backends.disabled", base.Backends.Disabled, got.Backends.Disabled, 1)
	assertDelta(t, "backends.read_only", base.Backends.ReadOnly, got.Backends.ReadOnly, 1)
	assertDelta(t, "backends.aws-s3", base.Backends.ByKind["aws-s3"], got.Backends.ByKind["aws-s3"], 1)

	assertDelta(t, "buckets.total", base.Buckets.Total, got.Buckets.Total, 2)
	// Every bucket has an owner (ADR-0032); a shared one is one it granted:
	// the fixture's is its tenant's alone, this test's is granted.
	assertDelta(t, "buckets.tenant_owned", base.Buckets.TenantOwned, got.Buckets.TenantOwned, 1)
	assertDelta(t, "buckets.shared", base.Buckets.Shared, got.Buckets.Shared, 1)
	assertDelta(t, "buckets.versioning", base.Buckets.VersioningEnabled, got.Buckets.VersioningEnabled, 1)
	assertDelta(t, "buckets.failed", base.Buckets.ByProvisionState["failed"], got.Buckets.ByProvisionState["failed"], 1)
	assertDelta(t, "buckets.ready", base.Buckets.ByProvisionState["ready"], got.Buckets.ByProvisionState["ready"], 1)

	assertDelta(t, "collections.total", base.Collections.Total, got.Collections.Total, 1)
}

// TestCollectRLSObjects proves the per-tenant / per-state object census:
// two tenants, several states, byte sums that tolerate the NULL size_bytes
// a PENDING row carries, and the count-descending tenant ordering the
// console's table relies on.
func TestCollectRLSObjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	big := seedFixture(t, ctx, pool)
	small := seedFixture(t, ctx, pool)

	insert := func(tenantID uuid.UUID, collection, state string, size any) {
		t.Helper()
		mustExec(t, ctx, pool,
			`INSERT INTO objects (id, tenant_id, collection_id, path, state, content_type, checksum_algorithm, size_bytes)
		 SELECT $1, $2, c.id, $4, $5, 'application/octet-stream', 0, $6
		   FROM collections c WHERE c.tenant_id = $2 AND c.name = $3`,
			uuid.Must(uuid.NewV7()), tenantID, collection, "k-"+uuid.NewString()[:8], state, size)
	}
	insert(big.tenantID, big.collection, "AVAILABLE", 100)
	insert(big.tenantID, big.collection, "AVAILABLE", 200)
	insert(big.tenantID, big.collection, "PENDING", nil) // size_bytes NULL until commit
	insert(big.tenantID, big.collection, "DELETED", 50)
	insert(small.tenantID, small.collection, "AVAILABLE", 7)

	census, err := platformstats.CollectRLS(ctx, pool, platformstats.TenantPage{})
	if err != nil {
		t.Fatalf("CollectRLS: %v", err)
	}
	got := census.Objects

	byTenant := map[string]platformstats.TenantStat{}
	for _, tn := range got.Tenants {
		byTenant[tn.TenantID] = tn
	}
	b := byTenant[big.tenantID.String()]
	if b.TotalCount != 4 || b.TotalBytes != 350 {
		t.Errorf("big tenant = %d objects / %d bytes, want 4/350", b.TotalCount, b.TotalBytes)
	}
	s := byTenant[small.tenantID.String()]
	if s.TotalCount != 1 || s.TotalBytes != 7 {
		t.Errorf("small tenant = %d objects / %d bytes, want 1/7", s.TotalCount, s.TotalBytes)
	}

	// Per-state, in lifecycle order (PENDING, AVAILABLE, DELETED)
	// with the absent states omitted.
	wantStates := []platformstats.StateStat{
		{State: "PENDING", Count: 1, Bytes: 0},
		{State: "AVAILABLE", Count: 2, Bytes: 300},
		{State: "DELETED", Count: 1, Bytes: 50},
	}
	if len(b.States) != len(wantStates) {
		t.Fatalf("big tenant states = %+v, want %+v", b.States, wantStates)
	}
	for i, w := range wantStates {
		if b.States[i] != w {
			t.Errorf("state[%d] = %+v, want %+v", i, b.States[i], w)
		}
	}

	// Tenants sort by object count descending so the server-side cap trims
	// the quiet tail, not the busy head.
	if len(got.Tenants) < 2 || got.Tenants[0].TotalCount < got.Tenants[1].TotalCount {
		t.Errorf("tenant order = %+v, want count-descending", got.Tenants)
	}
}

func assertDelta(t *testing.T, label string, before, after, want int64) {
	t.Helper()
	if got := after - before; got != want {
		t.Errorf("%s delta = %d, want %d (before=%d after=%d)", label, got, want, before, after)
	}
}

// TestCollectRLSPagesTheTenants walks the per-tenant breakdown one tenant per
// page against the real GROUP BY: each of this test's tenants appears exactly
// once, the busier first, and the walk ends. Other tests share the database,
// so only these two tenants are asserted on.
func TestCollectRLSPagesTheTenants(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	big := seedFixture(t, ctx, pool)
	small := seedFixture(t, ctx, pool)

	insert := func(tenantID uuid.UUID, collection string) {
		t.Helper()
		mustExec(t, ctx, pool,
			`INSERT INTO objects (id, tenant_id, collection_id, path, state, content_type, checksum_algorithm, size_bytes)
		 SELECT $1, $2, c.id, $4, 'AVAILABLE', 'application/octet-stream', 0, 1
		   FROM collections c WHERE c.tenant_id = $2 AND c.name = $3`,
			uuid.Must(uuid.NewV7()), tenantID, collection, "k-"+uuid.NewString()[:8])
	}
	insert(big.tenantID, big.collection)
	insert(big.tenantID, big.collection)
	insert(small.tenantID, small.collection)

	const maxPages = 10_000 // far past any fleet a shared test database holds
	var order []string
	page := platformstats.TenantPage{Size: 1}
	for n := 0; ; n++ {
		if n == maxPages {
			t.Fatal("the walk did not end")
		}
		census, err := platformstats.CollectRLS(ctx, pool, page)
		if err != nil {
			t.Fatalf("CollectRLS: %v", err)
		}
		if len(census.Objects.Tenants) > 1 {
			t.Fatalf("page of %d tenants, want at most 1", len(census.Objects.Tenants))
		}
		for _, tn := range census.Objects.Tenants {
			if tn.TenantID == big.tenantID.String() || tn.TenantID == small.tenantID.String() {
				order = append(order, tn.TenantID)
			}
		}
		if census.Objects.TenantsNext == "" {
			break
		}
		page.After = census.Objects.TenantsNext
	}
	want := []string{big.tenantID.String(), small.tenantID.String()}
	if len(order) != len(want) || order[0] != want[0] || order[1] != want[1] {
		t.Errorf("walked %v, want %v — each once, busier first", order, want)
	}
}

// TestCollectRLSSiblings proves the four aggregates that ride alongside the
// object census: quotas (scope split + cap proximity), capabilities and API
// tokens (the exclusive active/expired/revoked classification), and event
// subscriptions (enabled split + sink mix).
//
// Each block asserts on an absolute count because `seedFixture` seeds one
// event subscription and nothing else in these tables — the quota /
// capability / token rows below are the only ones that exist.
func TestCollectRLSSiblings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool) // seeds 1 enabled http subscription
	hex := uuid.NewString()[:8]

	// ─── quotas ─────────────────────────────────────────────────────────
	// Tenant-scoped row sitting exactly on its object cap → at_limit.
	mustExec(t, ctx, pool,
		`INSERT INTO quotas (id, tenant_id, max_object_count, max_total_bytes,
		                     usage_object_count, usage_total_bytes)
		 VALUES ($1, $2, 10, 0, 10, 4096)`, uuid.New(), f.tenantID)
	// Bucket-scoped row at 95% of its byte cap → near_limit, not at_limit.
	// Its usage must NOT land in the tenant-scoped usage sums.
	mustExec(t, ctx, pool,
		`INSERT INTO buckets (backend_id, name, owner_tenant_id)
		 SELECT sb.id, $2, $3 FROM storage_backends sb WHERE sb.name = $1`,
		backendOf(t, ctx, pool, f), "bkt-q-"+hex, f.tenantID)
	mustExec(t, ctx, pool,
		`INSERT INTO bucket_quotas (id, bucket_id, max_total_bytes, usage_total_bytes)
		 VALUES ($1, (SELECT b.id FROM buckets b
		          JOIN storage_backends sb ON sb.id = b.backend_id
		         WHERE sb.name = $2 AND b.name = $3), 1000, 950)`,
		uuid.New(), backendOf(t, ctx, pool, f), "bkt-q-"+hex)

	// ─── capabilities ───────────────────────────────────────────────────
	now := time.Now()
	active := seedCapability(t, ctx, pool, f.tenantID, limes.PrincipalTypeService, now.Add(48*time.Hour), uuid.Nil)
	seedCapability(t, ctx, pool, f.tenantID, limes.PrincipalTypeService, now.Add(-time.Hour), uuid.Nil)
	revoked := seedCapability(t, ctx, pool, f.tenantID, limes.PrincipalTypeUser, now.Add(48*time.Hour), uuid.Nil)
	// Delegated child of the active one, expiring inside the 24h window.
	seedCapability(t, ctx, pool, f.tenantID, limes.PrincipalTypeService, now.Add(time.Hour), active)
	seedRevocation(t, ctx, pool, revoked)

	// ─── api tokens ─────────────────────────────────────────────────────
	insertToken := func(expiresIn string, revokedAt any, lastUsed any) {
		t.Helper()
		// token_hmac (`001_initial_schema.sql`) is the token digest column.
		// Derive a distinct value per row from a fresh UUID.
		id := uuid.New()
		hmac := id[:] // 16 distinct bytes per row
		mustExec(t, ctx, pool,
			`INSERT INTO api_tokens (id, tenant_id, name, prefix, token_hmac,
			                         audience, created_by, expires_at,
			                         revoked_at, last_used_at)
			 VALUES ($1, $2, $3, 'pfx', $4, '{admin}', 'fixture-issuer',
			         now() + $5::interval, $6, $7)`,
			id, f.tenantID, "tok-"+uuid.NewString()[:8], hmac, expiresIn,
			revokedAt, lastUsed)
	}
	insertToken("720 hours", nil, nil)   // active, never used
	insertToken("48 hours", nil, "now")  // active, used, expiring soon (<7d)
	insertToken("-1 hour", nil, nil)     // expired
	insertToken("720 hours", "now", nil) // revoked (beats "active")

	// ─── subscriptions ──────────────────────────────────────────────────
	mustExec(t, ctx, pool,
		`INSERT INTO event_subscriptions (id, tenant_id, cel_filter,
		                                  sink_kind, sink_config, disabled)
		 VALUES ($1, $2, 'kind == "object.created"', 'nats', '{}'::jsonb, false)`,
		uuid.New(), f.tenantID)
	mustExec(t, ctx, pool,
		`INSERT INTO event_subscriptions (id, tenant_id, cel_filter,
		                                  sink_kind, sink_config, disabled)
		 VALUES ($1, $2, '', 'http', '{}'::jsonb, true)`,
		uuid.New(), f.tenantID)

	got, err := platformstats.CollectRLS(ctx, pool, platformstats.TenantPage{})
	if err != nil {
		t.Fatalf("CollectRLS: %v", err)
	}

	q := got.Quotas
	if q.Total != 2 || q.TenantScoped != 1 || q.BucketScoped != 1 {
		t.Errorf("quota scope split = %+v, want 2 total / 1 tenant / 1 bucket", q)
	}
	if q.WithLimits != 2 {
		t.Errorf("quota with_limits = %d, want 2", q.WithLimits)
	}
	if q.AtLimit != 1 {
		t.Errorf("quota at_limit = %d, want 1 (usage == max_object_count)", q.AtLimit)
	}
	if q.NearLimit != 1 {
		t.Errorf("quota near_limit = %d, want 1 (95%% of the byte cap)", q.NearLimit)
	}
	// Tenant-scoped only: the bucket row's 950 bytes must not be summed in.
	if q.UsageObjectCount != 10 || q.UsageTotalBytes != 4096 {
		t.Errorf("quota usage = %d objects / %d bytes, want 10/4096",
			q.UsageObjectCount, q.UsageTotalBytes)
	}

	c := got.Capabilities
	if c.Total != 4 {
		t.Fatalf("capabilities total = %d, want 4", c.Total)
	}
	// Exclusive and exhaustive: revocation beats expiry, expiry beats active.
	if c.Active+c.Expired+c.Revoked != c.Total {
		t.Errorf("capability dispositions %d+%d+%d != total %d",
			c.Active, c.Expired, c.Revoked, c.Total)
	}
	if c.Active != 2 || c.Expired != 1 || c.Revoked != 1 {
		t.Errorf("capabilities = %+v, want 2 active / 1 expired / 1 revoked", c)
	}
	if c.Delegated != 1 {
		t.Errorf("capabilities delegated = %d, want 1", c.Delegated)
	}
	if c.ExpiringSoon != 1 {
		t.Errorf("capabilities expiring_soon = %d, want 1 (the 1h child)", c.ExpiringSoon)
	}
	// by_principal_kind counts ACTIVE rows only — the expired
	// service_account and the revoked user must not appear.
	if c.ByPrincipalKind[string(limes.PrincipalTypeService)] != 2 || c.ByPrincipalKind[string(limes.PrincipalTypeUser)] != 0 {
		t.Errorf("capabilities by kind = %v, want service:2 only",
			c.ByPrincipalKind)
	}

	a := got.APITokens
	if a.Total != 4 || a.Active != 2 || a.Expired != 1 || a.Revoked != 1 {
		t.Errorf("api tokens = %+v, want 4 total / 2 active / 1 expired / 1 revoked", a)
	}
	if a.ExpiringSoon != 1 {
		t.Errorf("api tokens expiring_soon = %d, want 1 (the 48h one)", a.ExpiringSoon)
	}
	// The revoked token was also never used; never_used counts ACTIVE only.
	if a.NeverUsed != 1 {
		t.Errorf("api tokens never_used = %d, want 1 (active only)", a.NeverUsed)
	}

	sub := got.Subscriptions
	if sub.Total != 3 || sub.Enabled != 2 || sub.Disabled != 1 {
		t.Errorf("subscriptions = %+v, want 3 total / 2 enabled / 1 disabled", sub)
	}
	if sub.WithFilter != 1 {
		t.Errorf("subscriptions with_filter = %d, want 1", sub.WithFilter)
	}
	// Sink mix counts ENABLED rows: the fixture's http one plus our nats
	// one; the disabled http row is excluded.
	if sub.BySinkKind["http"] != 1 || sub.BySinkKind["nats"] != 1 {
		t.Errorf("subscriptions by sink = %v, want http:1 nats:1", sub.BySinkKind)
	}
}

// backendOf returns the backend_id seedFixture bound the object key to.
func backendOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, f fixture) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx,
		`SELECT sb.name
		   FROM collections c
		   JOIN buckets b           ON b.id = c.bucket_id
		   JOIN storage_backends sb ON sb.id = b.backend_id
		  WHERE c.tenant_id = $1 AND c.name = $2`,
		f.tenantID, f.collection).Scan(&id); err != nil {
		t.Fatalf("lookup backend: %v", err)
	}
	return id
}

// A child of a revoked capability counts as revoked, not active, since the
// verifier refuses it with its parent; and it leaves the expiring-soon count,
// which only active records are in.
func TestCollectRLSCountsAChildOfARevokedCapabilityAsRevoked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	now := time.Now()
	parent := seedCapability(t, ctx, pool, f.tenantID, limes.PrincipalTypeService, now.Add(48*time.Hour), uuid.Nil)
	seedCapability(t, ctx, pool, f.tenantID, limes.PrincipalTypeAgent, now.Add(time.Hour), parent)
	seedRevocation(t, ctx, pool, parent)

	got, err := platformstats.CollectRLS(ctx, pool, platformstats.TenantPage{})
	if err != nil {
		t.Fatalf("CollectRLS: %v", err)
	}
	c := got.Capabilities
	if c.Total != 2 || c.Revoked != 2 || c.Active != 0 || c.Delegated != 1 || c.ExpiringSoon != 0 {
		t.Errorf("capability census = %+v, want 2 revoked of 2, 1 delegated, none active or expiring", c)
	}
	if len(c.ByPrincipalKind) != 0 {
		t.Errorf("active by principal kind = %v, want none", c.ByPrincipalKind)
	}
}
