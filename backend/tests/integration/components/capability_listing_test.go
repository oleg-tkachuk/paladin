//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
)

// CapabilityService.List reads a tenant's records through the census, on the
// application role: an operator acting for a tenant lists that tenant's
// records, each under its disposition, and no other tenant's; a caller
// scoped to one tenant is refused another's, not shown an empty list.
func TestCapabilityListingIsTenantScoped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	platform, _ := mkTenant(t, ctx, admin, "shared")
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	other, _ := mkTenant(t, ctx, admin, "shared")

	seed := newCapStore(t, admin)
	active := mkCap(tenant, "user:active", time.Now().Add(time.Hour))
	expired := mkCap(tenant, "user:expired", time.Now().Add(-time.Minute))
	revoked := mkCap(tenant, "user:revoked", time.Now().Add(time.Hour))
	foreign := mkCap(other, "user:foreign", time.Now().Add(time.Hour))
	for _, c := range []limes.Capability{active, expired, revoked, foreign} {
		if err := seed.Insert(ctx, limes.InsertRequest{Capability: c, IssuedBy: seedIssuer}); err != nil {
			t.Fatalf("seed %s: %v", c.Subject.Subject, err)
		}
	}
	if err := seed.Revoke(auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenant}), limes.RevokeRequest{
		CapabilityID: revoked.ID, Note: "leaked", Actor: "user:operator",
	}); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	records, err := capstore.NewCensus(rlsPool(t, ctx, admin))
	if err != nil {
		t.Fatalf("new census: %v", err)
	}
	operator := auth.WithActingTenant(auth.WithPrincipal(ctx, &auth.Principal{TenantID: platform}), tenant)
	page, err := records.ListRecords(operator, limes.ListRecordsRequest{TenantID: tenant})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := map[uuid.UUID]limes.RecordDisposition{
		active.ID:  limes.RecordDispositionActive,
		expired.ID: limes.RecordDispositionExpired,
		revoked.ID: limes.RecordDispositionRevoked,
	}
	got := map[uuid.UUID]limes.RecordDisposition{}
	for _, l := range page.Items {
		got[l.Record.Capability.ID] = l.Disposition
	}
	if len(got) != len(want) {
		t.Fatalf("listed %v, want %v", got, want)
	}
	for id, d := range want {
		if got[id] != d {
			t.Errorf("%s listed as %q, want %q", id, got[id], d)
		}
	}

	scopedToOther := auth.WithPrincipal(ctx, &auth.Principal{TenantID: other})
	if _, err := records.ListRecords(scopedToOther, limes.ListRecordsRequest{TenantID: tenant}); !errors.Is(err, limes.ErrScopeMismatch) {
		t.Errorf("another tenant's listing: err = %v, want ErrScopeMismatch", err)
	}
}
