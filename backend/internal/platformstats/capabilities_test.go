package platformstats

import (
	"maps"
	"slices"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/limes"
)

// The census sums the counts of every principal type, and keeps by type
// only the active records.
func TestFoldCapabilities(t *testing.T) {
	got := foldCapabilities([]limes.RecordCount{
		{PrincipalType: limes.PrincipalTypeAgent, Total: 6, Active: 3, Expired: 1, Revoked: 2, Delegated: 2, ExpiringSoon: 1},
		{PrincipalType: limes.PrincipalTypeUser, Total: 2, Expired: 2},
		{PrincipalType: limes.PrincipalTypeService, Total: 1, Active: 1},
	})
	want := CapabilityCensus{
		Total: 9, Active: 4, Expired: 3, Revoked: 2, Delegated: 2, ExpiringSoon: 1,
		ByPrincipalKind: map[string]int64{string(limes.PrincipalTypeAgent): 3, string(limes.PrincipalTypeService): 1},
	}
	if got.Total != want.Total || got.Active != want.Active || got.Expired != want.Expired ||
		got.Revoked != want.Revoked || got.Delegated != want.Delegated || got.ExpiringSoon != want.ExpiringSoon {
		t.Errorf("census = %+v, want %+v", got, want)
	}
	if !maps.Equal(got.ByPrincipalKind, want.ByPrincipalKind) {
		t.Errorf("by principal kind = %v, want %v", got.ByPrincipalKind, want.ByPrincipalKind)
	}
}

// No record is an empty census whose map is there to read, not nil.
func TestFoldCapabilitiesOfNone(t *testing.T) {
	if got := foldCapabilities(nil); got.Total != 0 || got.ByPrincipalKind == nil {
		t.Errorf("census of none = %+v", got)
	}
}

// The drill-down names each tenant with records expiring soon, and only
// those, so its counts sum to the card's.
func TestExpiringByTenant(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	got := expiringByTenant([]limes.RecordCount{
		{TenantID: a, Active: 5, ExpiringSoon: 2},
		{TenantID: b, Active: 3},
		{TenantID: c, Active: 1, ExpiringSoon: 1},
	})
	want := []TenantCount{{TenantID: a.String(), Count: 2}, {TenantID: c.String(), Count: 1}}
	if !slices.Equal(got, want) {
		t.Errorf("tenants = %+v, want %+v", got, want)
	}
}
