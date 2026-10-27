package admin

import (
	"context"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// trashList answers ListTenants with a fixed page.
type trashList struct {
	failingTenant
	page []tenanth.Tenant
}

func (f trashList) ListTenants(context.Context, tenanth.ListTenantsArgs, string) ([]tenanth.Tenant, string, error) {
	return f.page, "", nil
}

func (f trashList) GetTenant(_ context.Context, id uuid.UUID) (*tenanth.Tenant, error) {
	for i := range f.page {
		if f.page[i].TenantID == id {
			return &f.page[i], nil
		}
	}
	return nil, tenanth.ErrNotFound
}

// purge_eligible_at is deleted_at + the configured retention for a trashed
// tenant, and unset for an active one — on every read RPC, not only List.
func TestTenantPurgeEligibleAtOnTheWire(t *testing.T) {
	const retention = 7 * 24 * time.Hour
	deleted := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	trashedID, activeID := uuid.New(), uuid.New()
	srv := NewTenantServer(nil, retention)
	srv.H = trashList{page: []tenanth.Tenant{
		{TenantID: trashedID, Slug: "gone", DeletedAt: deleted},
		{TenantID: activeID, Slug: "live"},
	}}

	resp, err := srv.ListTenants(t.Context(), &pb.ListTenantsRequest{IncludeTrashed: true})
	if err != nil {
		t.Fatalf("ListTenants: %v", err)
	}
	if len(resp.GetTenants()) != 2 {
		t.Fatalf("got %d tenants, want 2", len(resp.GetTenants()))
	}
	trashed, active := resp.GetTenants()[0], resp.GetTenants()[1]
	if got, want := trashed.GetPurgeEligibleAt().AsTime(), deleted.Add(retention); !got.Equal(want) {
		t.Errorf("trashed purge_eligible_at = %v, want %v", got, want)
	}
	if !trashed.GetDeletedAt().AsTime().Equal(deleted) {
		t.Errorf("trashed deleted_at = %v, want %v", trashed.GetDeletedAt().AsTime(), deleted)
	}
	if active.GetPurgeEligibleAt() != nil {
		t.Errorf("active tenant carries purge_eligible_at %v", active.GetPurgeEligibleAt())
	}

	got, err := srv.GetTenant(t.Context(), &pb.GetTenantRequest{Name: "tenants/" + trashedID.String()})
	if err != nil {
		t.Fatalf("GetTenant: %v", err)
	}
	if !got.GetPurgeEligibleAt().AsTime().Equal(deleted.Add(retention)) {
		t.Errorf("GetTenant purge_eligible_at = %v", got.GetPurgeEligibleAt())
	}

	// No retention wired (a zero-valued server) leaves the field unset rather
	// than reporting the tenant due the instant it was trashed.
	bare := &TenantServer{H: srv.H}
	resp, err = bare.ListTenants(t.Context(), &pb.ListTenantsRequest{OnlyTrashed: true})
	if err != nil {
		t.Fatalf("ListTenants: %v", err)
	}
	if resp.GetTenants()[0].GetPurgeEligibleAt() != nil {
		t.Error("zero retention produced a purge_eligible_at")
	}
}
