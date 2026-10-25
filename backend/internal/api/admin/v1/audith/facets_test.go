package audith

import (
	"errors"
	"slices"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
)

// A plane the facet tests narrow by.
const testPlane = "paladin-iam"

// The counts are of the listing the same arguments list: the caller held to
// its own tenant, the filter's conjuncts pushed down, the facets as asked —
// and refused where the listing is.
func TestCountAuditFacets(t *testing.T) {
	tenant := uuid.New()
	want := []admindomain.AuditFacetCount{{Facet: admindomain.AuditFacetPlane, Value: testPlane, Count: 4}}
	facets := admindomain.AuditFacets{Planes: []string{testPlane}, Outcomes: []admindomain.AuditOutcome{admindomain.AuditOutcomeError}}

	repo := &fakeAuditRepo{counts: want}
	got, err := NewHandler(repo, allowAuthorizer{}).CountAuditFacets(ctxAsTenantAdmin(tenant),
		admindomain.ListAuditArgs{Facets: facets}, `actor_subject == "alice"`)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("counts %v, want the store's %v", got, want)
	}
	a := repo.counted
	if a == nil || a.ActorTenantID != tenant || a.ActorSubject != "alice" ||
		!slices.Equal(a.Facets.Planes, facets.Planes) || !slices.Equal(a.Facets.Outcomes, facets.Outcomes) {
		t.Errorf("store asked for %+v", a)
	}

	for name, tc := range map[string]struct {
		h    *Handler
		args admindomain.ListAuditArgs
		code connect.Code
	}{
		"another tenant's trail": {NewHandler(&fakeAuditRepo{}, allowAuthorizer{}), admindomain.ListAuditArgs{TrailTenantID: uuid.New()}, connect.CodePermissionDenied},
		"policy denies":          {NewHandler(&fakeAuditRepo{}, denyAuthorizer{}), admindomain.ListAuditArgs{}, connect.CodePermissionDenied},
	} {
		if _, err := tc.h.CountAuditFacets(ctxAsTenantAdmin(tenant), tc.args, ""); connect.CodeOf(err) != tc.code {
			t.Errorf("%s: code %v, want %v", name, connect.CodeOf(err), tc.code)
		}
	}
	if _, err := NewHandler(&fakeAuditRepo{}, allowAuthorizer{}).CountAuditFacets(ctxAsTenantAdmin(tenant), admindomain.ListAuditArgs{}, "not cel ((("); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a bad filter: %v, want InvalidArgument", err)
	}

	boom := errors.New("boom")
	failing := &fakeAuditRepo{countErr: boom}
	if _, err := NewHandler(failing, allowAuthorizer{}).CountAuditFacets(ctxWithPlatformAdmin(t), admindomain.ListAuditArgs{}, ""); !errors.Is(err, boom) || connect.CodeOf(err) != connect.CodeInternal {
		t.Errorf("a store error: %v, want it as Internal", err)
	}
}
