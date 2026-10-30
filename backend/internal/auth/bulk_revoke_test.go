package auth

import (
	"context"
	"errors"
	"slices"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
)

// fakeBulk revokes in batches — More until the last — naming tenants, and
// records each release with the tenant its context acts on.
type fakeBulk struct {
	// everyTenant records whether each revoke, then each release, was
	// scoped to write every tenant.
	everyTenant []bool
	batches     int
	tenants     []uuid.UUID
	revokes     int
	revokeErr   error
	releases    []meter.ReleaseMatchingRequest
	releasedIn  []uuid.UUID
	releaseErr  error
}

func (f *fakeBulk) RevokeMatching(ctx context.Context, _ limes.RevokeMatchingRequest) (limes.RevokeMatchingResult, error) {
	f.everyTenant = append(f.everyTenant, AllTenantsWrite(ctx))
	f.revokes++
	if f.revokeErr != nil {
		return limes.RevokeMatchingResult{}, f.revokeErr
	}
	return limes.RevokeMatchingResult{Revoked: 1, Tenants: f.tenants, More: f.revokes < f.batches}, nil
}

func (f *fakeBulk) ReleaseMatching(ctx context.Context, req meter.ReleaseMatchingRequest) (meter.ReleaseMatchingResult, error) {
	f.everyTenant = append(f.everyTenant, AllTenantsWrite(ctx))
	f.releases = append(f.releases, req)
	t, _ := ActingTenant(ctx)
	f.releasedIn = append(f.releasedIn, t)
	return meter.ReleaseMatchingResult{Released: 1}, f.releaseErr
}

// A revocation runs batch after batch, then releases the holds of what it
// revoked by the same selectors, in each tenant it wrote in, acting there.
func TestBulkRevocationReleasesTheHoldsOfWhatItRevoked(t *testing.T) {
	const batches = 3
	tenant, other := uuid.New(), uuid.New()
	f := &fakeBulk{batches: batches, tenants: []uuid.UUID{tenant, other}}
	b := &BulkRevocation{Revoker: f, Holds: f}

	revoked, err := b.RevokeMatching(context.Background(), limes.RevokeMatchingRequest{TenantID: tenant, Session: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if revoked != batches || f.revokes != batches {
		t.Errorf("revoked %d in %d calls, want %d in %d", revoked, f.revokes, batches, batches)
	}
	if len(f.releases) != 2 || f.releasedIn[0] != tenant || f.releasedIn[1] != other {
		t.Fatalf("released in %v, want %s then %s", f.releasedIn, tenant, other)
	}
	if r := f.releases[0]; r.TenantID != tenant || r.Session != "s1" {
		t.Errorf("release = %+v, want tenant %s session s1", r, tenant)
	}
}

// A tenant's holds are released even when this call wrote nothing there: a
// retry after a release failed finds every capability already revoked.
func TestBulkRevocationReleasesOnARetry(t *testing.T) {
	tenant := uuid.New()
	f := &fakeBulk{batches: 1}
	b := &BulkRevocation{Revoker: f, Holds: f}
	if _, err := b.RevokeSession(context.Background(), tenant, uuid.New(), "note", "actor"); err != nil {
		t.Fatal(err)
	}
	if len(f.releasedIn) != 1 || f.releasedIn[0] != tenant {
		t.Errorf("released in %v, want %s", f.releasedIn, tenant)
	}
}

// A failure stops before what depends on it, and says so.
func TestBulkRevocationFailures(t *testing.T) {
	errStore := errors.New("pgstore: connection refused")
	revokeFails := &fakeBulk{revokeErr: errStore}
	if _, err := (&BulkRevocation{Revoker: revokeFails, Holds: revokeFails}).RevokeMatching(context.Background(), limes.RevokeMatchingRequest{TenantID: uuid.New(), ClientID: "c"}); !errors.Is(err, errStore) || len(revokeFails.releases) != 0 {
		t.Errorf("revoke failing: err = %v after %d releases, want it and none", err, len(revokeFails.releases))
	}
	releaseFails := &fakeBulk{batches: 1, releaseErr: errStore}
	if _, err := (&BulkRevocation{Revoker: releaseFails, Holds: releaseFails}).RevokeMatching(context.Background(), limes.RevokeMatchingRequest{TenantID: uuid.New(), ClientID: "c"}); !errors.Is(err, errStore) {
		t.Errorf("release failing: err = %v, want it", err)
	}
}

// With metering off there are no holds to release, and with the capability
// subsystem off nothing to revoke.
func TestBulkRevocationWithoutItsParts(t *testing.T) {
	f := &fakeBulk{batches: 1}
	if n, err := (&BulkRevocation{Revoker: f}).RevokeMatching(context.Background(), limes.RevokeMatchingRequest{TenantID: uuid.New(), ClientID: "c"}); err != nil || n != 1 {
		t.Errorf("no holds: %d, %v; want 1 revoked", n, err)
	}
	var off *BulkRevocation
	if n, err := off.RevokeMatching(context.Background(), limes.RevokeMatchingRequest{}); err != nil || n != 0 {
		t.Errorf("off: %d, %v; want nothing", n, err)
	}
}

// A revocation across tenants is scoped to write every tenant for its own
// calls alone; each tenant's holds are released acting on that tenant.
func TestBulkRevocationAcrossTenantsScopesOnlyTheRevocation(t *testing.T) {
	tenant := uuid.New()
	f := &fakeBulk{batches: 1, tenants: []uuid.UUID{tenant}}
	b := &BulkRevocation{Revoker: f, Holds: f}
	ctx := WithActingTenant(context.Background(), uuid.New())
	if _, err := b.RevokeMatching(ctx, limes.RevokeMatchingRequest{AllTenants: true, ClientID: "agent"}); err != nil {
		t.Fatal(err)
	}
	if want := []bool{true, false}; !slices.Equal(f.everyTenant, want) {
		t.Errorf("every-tenant scope per call = %v, want %v", f.everyTenant, want)
	}
	if len(f.releasedIn) != 1 || f.releasedIn[0] != tenant {
		t.Errorf("released in %v, want only %s, where the revocation wrote", f.releasedIn, tenant)
	}
}
