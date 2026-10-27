//go:build integration

package components

import (
	"context"
	"maps"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// The actions and planes the facet test records, and the services
// audit_service names them by.
const (
	facetBucketAction   = "/paladin.admin.v1.BucketService/CreateBucket"
	facetBucketService  = "paladin.admin.v1.BucketService"
	facetLoginAction    = "/paladin.iam.v1.AuthService/Login"
	facetLoginService   = "paladin.iam.v1.AuthService"
	facetBootstrapAudit = "iam.bootstrap_admin"
)

// Each facet is counted under every other constraint and without its own
// choices, and the listing honours all of them, under row-level security.
func TestCountAuditFacets_EachWithoutItsOwnChoices(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	pool := rlsPool(t, ctx, admin)
	repo := adapters.NewAuditRepoV2(sqlc.New(pool), pool)
	tenant := uuid.New()
	mustExec(t, ctx, admin, `INSERT INTO tenants (id, slug, display_name) VALUES ($1, $2, $2)`, tenant, "f-"+tenant.String()[:8])
	asTenant := auth.WithActingTenant(ctx, tenant)

	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i, e := range []struct {
		audience, action, failure string
	}{
		{auth.AudienceAdmin, facetBucketAction, ""},
		{auth.AudienceAdmin, facetBucketAction, "bucket exists"},
		{auth.AudienceAdmin, facetBucketAction, ""},
		{auth.AudienceIAM, facetLoginAction, "invalid credentials"},
		{auth.AudienceIAM, facetBootstrapAudit, ""},
	} {
		if err := repo.Insert(asTenant, admindomain.AuditEntry{
			At: at.Add(time.Duration(i) * time.Second), ActorSubject: "alice", ActorTenantID: tenant,
			ActorAudience: e.audience, Action: e.action, ErrorMessage: e.failure,
		}); err != nil {
			t.Fatal(err)
		}
	}
	ok, failed := string(admindomain.AuditOutcomeOK), string(admindomain.AuditOutcomeError)
	allPlanes := map[string]int64{auth.AudienceAdmin: 3, auth.AudienceIAM: 2}
	allOutcomes := map[string]int64{ok: 3, failed: 2}
	allServices := map[string]int64{facetBucketService: 3, facetLoginService: 1, facetBootstrapAudit: 1}

	for _, tc := range []struct {
		name                       string
		facets                     admindomain.AuditFacets
		planes, outcomes, services map[string]int64
		listed                     int
	}{
		{"none chosen", admindomain.AuditFacets{}, allPlanes, allOutcomes, allServices, 5},
		{"a plane", admindomain.AuditFacets{Planes: []string{auth.AudienceIAM}},
			allPlanes, map[string]int64{ok: 1, failed: 1}, map[string]int64{facetLoginService: 1, facetBootstrapAudit: 1}, 2},
		{"failures", admindomain.AuditFacets{Outcomes: []admindomain.AuditOutcome{admindomain.AuditOutcomeError}},
			map[string]int64{auth.AudienceAdmin: 1, auth.AudienceIAM: 1}, allOutcomes, map[string]int64{facetBucketService: 1, facetLoginService: 1}, 2},
		{"a service and successes", admindomain.AuditFacets{Services: []string{facetBucketService}, Outcomes: []admindomain.AuditOutcome{admindomain.AuditOutcomeOK}},
			map[string]int64{auth.AudienceAdmin: 2}, map[string]int64{ok: 2, failed: 1}, map[string]int64{facetBucketService: 2, facetBootstrapAudit: 1}, 2},
	} {
		args := admindomain.ListAuditArgs{ActorTenantID: tenant, Facets: tc.facets}
		counts, err := repo.CountFacets(asTenant, args)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]map[string]int64{admindomain.AuditFacetPlane: {}, admindomain.AuditFacetOutcome: {}, admindomain.AuditFacetService: {}}
		for _, c := range counts {
			got[c.Facet][c.Value] = c.Count
		}
		for facet, want := range map[string]map[string]int64{
			admindomain.AuditFacetPlane: tc.planes, admindomain.AuditFacetOutcome: tc.outcomes, admindomain.AuditFacetService: tc.services,
		} {
			if !maps.Equal(got[facet], want) {
				t.Errorf("%s: %s counts %v, want %v", tc.name, facet, got[facet], want)
			}
		}
		args.PageSize = 10
		entries, _, err := repo.List(asTenant, args)
		if err != nil || len(entries) != tc.listed {
			t.Errorf("%s: listed %d (%v), want %d", tc.name, len(entries), err, tc.listed)
		}
	}
}
