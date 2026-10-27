//go:build integration

package components

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/platformstats"
)

// The drill-down behind each flagged census count answers "whose", and it is
// only trustworthy if its answer adds up to the number on the card. These
// tests seed rows on both sides of every signal for two tenants, plus a quota
// on a shared bucket that no tenant owns, and hold that the tenants' counts
// plus the unattributed ones equal the census count on the same data.

// signalSeed is two tenants' worth of rows on both sides of every signal,
// plus the tenant owning the buckets neither of them owns.
type signalSeed struct {
	busy, quiet, owner uuid.UUID
}

func seedSignals(t *testing.T, ctx context.Context, pool *pgxpool.Pool) signalSeed {
	t.Helper()
	f := seedFixture(t, ctx, pool)
	quiet, _ := mkTenant(t, ctx, pool, "shared")
	owner, _ := mkTenant(t, ctx, pool, "shared")
	s := signalSeed{busy: f.tenantID, quiet: quiet, owner: owner}
	hex := uuid.New().String()[:8]
	backend := backendOf(t, ctx, pool, f)

	bucket := func(name string, owner uuid.UUID) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO buckets (backend_id, name, owner_tenant_id)
			 SELECT sb.id, $2, $3 FROM storage_backends sb WHERE sb.name = $1
			 RETURNING id::text`, backend, name, owner).Scan(&id); err != nil {
			t.Fatalf("seed bucket %s: %v", name, err)
		}
		return id
	}
	tenantQuota := func(tenant uuid.UUID, usage int64) {
		t.Helper()
		mustExec(t, ctx, pool,
			`INSERT INTO quotas (tenant_id, max_object_count, usage_object_count) VALUES ($1, 100, $2)`,
			tenant, usage)
	}
	bucketQuota := func(bucketID string, usage int64) {
		t.Helper()
		mustExec(t, ctx, pool,
			`INSERT INTO bucket_quotas (bucket_id, max_object_count, usage_object_count) VALUES ($1, 100, $2)`,
			bucketID, usage)
	}
	const (
		atLimit   = 100 // the cap itself
		nearLimit = 95  // inside the 90% band, below the cap
		healthy   = 10
	)
	// busy: at limit on its own quota and on a bucket it owns, near on a
	// second owned bucket.
	tenantQuota(s.busy, atLimit)
	bucketQuota(bucket("bkt-own-a-"+hex, s.busy), atLimit)
	bucketQuota(bucket("bkt-own-n-"+hex, s.busy), nearLimit)
	// quiet: near limit on its own quota only.
	tenantQuota(s.quiet, nearLimit)
	// Neither's: every bucket has an owner (ADR-0032), so the buckets that
	// were nobody's — one at its limit, one healthy — are a third tenant's.
	bucketQuota(bucket("bkt-shr-a-"+hex, s.owner), atLimit)
	bucketQuota(bucket("bkt-shr-h-"+hex, s.owner), healthy)

	now := time.Now()
	capability := func(tenant uuid.UUID, expiresIn time.Duration, revoked bool) {
		t.Helper()
		id := seedCapability(t, ctx, pool, tenant, limes.PrincipalTypeService, now.Add(expiresIn), uuid.Nil())
		if revoked {
			seedRevocation(t, ctx, pool, id)
		}
	}
	capability(s.busy, time.Hour, false)
	capability(s.busy, 2*time.Hour, false)
	capability(s.busy, 3*time.Hour, true) // revoked: not expiring, gone
	capability(s.busy, 48*time.Hour, false)
	capability(s.quiet, time.Hour, false)

	token := func(tenant uuid.UUID, expiresIn string) {
		t.Helper()
		id := uuid.New()
		mustExec(t, ctx, pool,
			`INSERT INTO api_tokens (id, tenant_id, name, prefix, token_hmac,
			                         audience, created_by, expires_at)
			 VALUES ($1, $2, $3, 'pfx', $4, '{admin}', 'fixture-issuer', now() + $5::interval)`,
			id, tenant, "tok-"+uuid.New().String()[:8], id[:], expiresIn)
	}
	token(s.quiet, "2 days")
	token(s.quiet, "3 days")
	token(s.busy, "1 day")
	token(s.busy, "30 days")
	return s
}

func TestCollectSignalTenants_AddsUpToTheCensus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	s := seedSignals(t, ctx, pool)

	census, err := platformstats.CollectRLS(ctx, pool, platformstats.TenantPage{})
	if err != nil {
		t.Fatalf("CollectRLS: %v", err)
	}

	cases := []struct {
		signal           platformstats.Signal
		census           int64
		want             []platformstats.TenantCount
		wantUnattributed int64
	}{
		// ADR-0032: decide whether Unattributed stays — with no ownerless
		// bucket nothing produces it, so the third tenant now takes the
		// shared bucket's at-limit quota that used to be unattributed.
		{platformstats.SignalQuotaAtLimit, census.Quotas.AtLimit,
			[]platformstats.TenantCount{{TenantID: s.busy.String(), Count: 2}, {TenantID: s.owner.String(), Count: 1}}, 0},
		{platformstats.SignalQuotaNearLimit, census.Quotas.NearLimit,
			ranked(s.busy, s.quiet, 1, 1), 0},
		{platformstats.SignalCapabilitiesExpiring, census.Capabilities.ExpiringSoon,
			[]platformstats.TenantCount{{TenantID: s.busy.String(), Count: 2}, {TenantID: s.quiet.String(), Count: 1}}, 0},
		{platformstats.SignalAPITokensExpiring, census.APITokens.ExpiringSoon,
			[]platformstats.TenantCount{{TenantID: s.quiet.String(), Count: 2}, {TenantID: s.busy.String(), Count: 1}}, 0},
	}
	for _, tc := range cases {
		t.Run(string(tc.signal), func(t *testing.T) {
			got, err := platformstats.CollectSignalTenants(ctx, pool, tc.signal, platformstats.TenantPage{})
			if err != nil {
				t.Fatalf("CollectSignalTenants: %v", err)
			}
			sum := got.Unattributed
			for _, tn := range got.Tenants {
				sum += tn.Count
			}
			if sum != tc.census {
				t.Errorf("tenants %+v + unattributed %d = %d, want the census count %d",
					got.Tenants, got.Unattributed, sum, tc.census)
			}
			if got.Unattributed != tc.wantUnattributed {
				t.Errorf("unattributed = %d, want %d", got.Unattributed, tc.wantUnattributed)
			}
			if len(got.Tenants) != len(tc.want) {
				t.Fatalf("tenants = %+v, want %+v", got.Tenants, tc.want)
			}
			for i := range tc.want {
				if got.Tenants[i] != tc.want[i] {
					t.Errorf("tenants[%d] = %+v, want %+v", i, got.Tenants[i], tc.want[i])
				}
			}
			if got.TenantsCut != 0 || got.TenantsNext != "" {
				t.Errorf("one page holds everything, got truncated=%d next=%q", got.TenantsCut, got.TenantsNext)
			}
		})
	}
}

// ranked orders two tenants with equal counts the way the drill-down does:
// by tenant id.
func ranked(a, b uuid.UUID, countA, countB int64) []platformstats.TenantCount {
	out := []platformstats.TenantCount{{TenantID: a.String(), Count: countA}, {TenantID: b.String(), Count: countB}}
	if countA == countB && b.String() < a.String() {
		out[0], out[1] = out[1], out[0]
	}
	return out
}

func TestCollectSignalTenants_PagesLikeTheObjectTable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	s := seedSignals(t, ctx, pool)

	first, err := platformstats.CollectSignalTenants(ctx, pool,
		platformstats.SignalCapabilitiesExpiring, platformstats.TenantPage{Size: 1})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first.Tenants) != 1 || first.Tenants[0].TenantID != s.busy.String() ||
		first.TenantsCut != 1 || first.TenantsNext == "" {
		t.Fatalf("first page = %+v, want busy, one more, a cursor", first)
	}
	second, err := platformstats.CollectSignalTenants(ctx, pool,
		platformstats.SignalCapabilitiesExpiring, platformstats.TenantPage{Size: 1, After: first.TenantsNext})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second.Tenants) != 1 || second.Tenants[0].TenantID != s.quiet.String() ||
		second.TenantsCut != 0 || second.TenantsNext != "" {
		t.Fatalf("second page = %+v, want quiet and the end", second)
	}
}

// The worker endpoint serves exactly what CollectSignalTenants returns.
func TestSignalTenantsHandler_ServesTheDrillDown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	s := seedSignals(t, ctx, pool)

	srv := httptest.NewServer(platformstats.SignalTenantsHandler(pool, func(err error) {
		t.Errorf("handler reported %v", err)
	}))
	defer srv.Close()

	q := platformstats.SignalQuery(platformstats.SignalQuotaAtLimit, platformstats.TenantPage{})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		srv.URL+platformstats.SignalTenantsPath+"?"+q.Encode(), nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %s", res.Status)
	}
	var got platformstats.SignalTenants
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// ADR-0032: decide whether Unattributed stays; no bucket is ownerless,
	// so the at-limit bucket that used to be unattributed is the owner's.
	if got.Signal != platformstats.SignalQuotaAtLimit || got.Unattributed != 0 ||
		len(got.Tenants) != 2 || got.Tenants[0].TenantID != s.busy.String() ||
		got.Tenants[1].TenantID != s.owner.String() {
		t.Errorf("served %+v, want busy then the bucket owner, nothing unattributed", got)
	}
}
