package auth

import (
	"context"
	"errors"
	"slices"
	"testing"
	"uuid"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// fakeBulk revokes, then releases, in batches — More until the last — and
// records each release with the tenant its context acts on.
type fakeBulk struct {
	// everyTenant records whether each revoke, then each release, was
	// scoped to write every tenant.
	everyTenant []bool
	batches     int
	revokes     int
	revokeReqs  []limes.RevokeMatchingRequest
	revokeErr   error
	// releaseBatches is how many release calls a release takes; one when
	// zero.
	releaseBatches int
	releases       []meter.ReleaseMatchingRequest
	releasedIn     []uuid.UUID
	releaseErr     error
	// released is how many holds each release reports; nothing makes every
	// revoke report none, as a repeat finds.
	released int64
	nothing  bool
}

func (f *fakeBulk) RevokeMatching(ctx context.Context, req limes.RevokeMatchingRequest) (limes.RevokeMatchingResult, error) {
	f.everyTenant = append(f.everyTenant, AllTenantsWrite(ctx))
	f.revokeReqs = append(f.revokeReqs, req)
	f.revokes++
	if f.revokeErr != nil {
		return limes.RevokeMatchingResult{}, f.revokeErr
	}
	if f.nothing {
		return limes.RevokeMatchingResult{}, nil
	}
	return limes.RevokeMatchingResult{Revoked: 1, More: f.revokes < f.batches}, nil
}

func (f *fakeBulk) ReleaseMatching(ctx context.Context, req meter.ReleaseMatchingRequest) (meter.ReleaseMatchingResult, error) {
	f.everyTenant = append(f.everyTenant, AllTenantsWrite(ctx))
	f.releases = append(f.releases, req)
	t, _ := ActingTenant(ctx)
	f.releasedIn = append(f.releasedIn, t)
	return meter.ReleaseMatchingResult{Released: f.released, More: len(f.releases) < f.releaseBatches}, f.releaseErr
}

// A revocation runs batch after batch, then releases, batch after batch,
// the holds of what it revoked by the same selectors, acting on its tenant.
func TestBulkRevocationReleasesTheHoldsOfWhatItRevoked(t *testing.T) {
	const batches, releaseBatches = 3, 2
	tenant := uuid.New()
	f := &fakeBulk{batches: batches, releaseBatches: releaseBatches}
	b := &BulkRevocation{Revoker: f, Holds: f}

	revoked, err := b.RevokeMatching(context.Background(), limes.RevokeMatchingRequest{TenantID: tenant, Session: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if revoked != batches || f.revokes != batches {
		t.Errorf("revoked %d in %d calls, want %d in %d", revoked, f.revokes, batches, batches)
	}
	if len(f.releases) != releaseBatches {
		t.Fatalf("%d release calls, want %d", len(f.releases), releaseBatches)
	}
	for i, r := range f.releases {
		if r.TenantID != tenant || r.AllTenants || r.Session != "s1" || f.releasedIn[i] != tenant {
			t.Errorf("release %d = %+v acting on %s, want tenant %s session s1", i, r, f.releasedIn[i], tenant)
		}
	}
}

// What a revocation stopped is logged — the capabilities and the holds,
// which no caller otherwise reports — at info, and a repeat that found
// nothing at debug.
func TestBulkRevocationLogsWhatItStopped(t *testing.T) {
	const batches = 2
	tenant := uuid.New()
	cases := map[string]struct {
		bulk         *fakeBulk
		level        zapcore.Level
		revoked      int64
		holdsRelease int64
	}{
		"something stopped": {&fakeBulk{batches: batches, releaseBatches: batches, released: 1}, zapcore.InfoLevel, batches, batches},
		"a retry releasing": {&fakeBulk{batches: 1, nothing: true, released: 1}, zapcore.InfoLevel, 0, 1},
		"a repeat":          {&fakeBulk{batches: 1, nothing: true}, zapcore.DebugLevel, 0, 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			ctx := logger.WithContext(context.Background(), zap.New(core))
			b := &BulkRevocation{Revoker: tc.bulk, Holds: tc.bulk}
			if _, err := b.RevokeMatching(ctx, limes.RevokeMatchingRequest{TenantID: tenant, Session: "s1"}); err != nil {
				t.Fatal(err)
			}
			lines := logs.FilterMessage("capabilities revoked in bulk").All()
			if len(lines) != 1 {
				t.Fatalf("%d lines, want 1", len(lines))
			}
			got := lines[0].ContextMap()
			if lines[0].Level != tc.level || got[LogKeyCapabilitiesRevoked] != tc.revoked ||
				got[logKeyHoldsReleased] != tc.holdsRelease || got["session"] != "s1" {
				t.Errorf("logged %v %v, want %v with %d revoked and %d holds released", lines[0].Level, got, tc.level, tc.revoked, tc.holdsRelease)
			}
		})
	}
}

// A failed release is the caller's to log: nothing claims it was released.
func TestBulkRevocationLogsNoOutcomeOnFailure(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContext(context.Background(), zap.New(core))
	f := &fakeBulk{batches: 1, releaseErr: errors.New("pgstore: connection refused")}
	if _, err := (&BulkRevocation{Revoker: f, Holds: f}).RevokeMatching(ctx, limes.RevokeMatchingRequest{TenantID: uuid.New(), ClientID: "c"}); err == nil {
		t.Fatal("want the release's error")
	}
	if logs.Len() != 0 {
		t.Errorf("logged %v, want nothing", logs.All())
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

// A revocation across tenants writes every tenant, and releases in every
// tenant by its selectors alone.
func TestBulkRevocationAcrossTenantsReleasesInEveryTenant(t *testing.T) {
	f := &fakeBulk{batches: 1}
	b := &BulkRevocation{Revoker: f, Holds: f}
	ctx := WithActingTenant(context.Background(), uuid.New())
	if _, err := b.RevokeMatching(ctx, limes.RevokeMatchingRequest{AllTenants: true, ClientID: "agent"}); err != nil {
		t.Fatal(err)
	}
	if want := []bool{true, true}; !slices.Equal(f.everyTenant, want) {
		t.Errorf("every-tenant scope per call = %v, want %v", f.everyTenant, want)
	}
	if len(f.releases) != 1 {
		t.Fatalf("%d release calls, want 1", len(f.releases))
	}
	if r := f.releases[0]; !r.AllTenants || r.TenantID != uuid.Nil() || r.ClientID != "agent" {
		t.Errorf("release = %+v, want every tenant's holds of client agent", r)
	}
}

// A retry after a release across tenants failed revokes nothing, and still
// releases in every tenant: the holds of what the first call revoked.
func TestBulkRevocationAcrossTenantsReleasesOnARetry(t *testing.T) {
	f := &fakeBulk{batches: 1, nothing: true, released: 1}
	b := &BulkRevocation{Revoker: f, Holds: f}
	n, err := b.RevokeMatching(context.Background(), limes.RevokeMatchingRequest{AllTenants: true, ClientID: "agent"})
	if err != nil || n != 0 {
		t.Fatalf("retry: %d revoked, %v; want none and no error", n, err)
	}
	if len(f.releases) != 1 || !f.releases[0].AllTenants {
		t.Errorf("releases = %+v, want one across every tenant", f.releases)
	}
}

// One revoked capability's holds are released, batch after batch, by its id
// alone, acting on its tenant, and the release is logged.
func TestReleaseRevokedReleasesOneCapabilitysHolds(t *testing.T) {
	const releaseBatches = 2
	tenant, capability := uuid.New(), uuid.New()
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContext(context.Background(), zap.New(core))
	f := &fakeBulk{releaseBatches: releaseBatches, released: 1}

	n, err := (&BulkRevocation{Revoker: f, Holds: f}).ReleaseRevoked(ctx, tenant, capability)
	if err != nil || n != releaseBatches {
		t.Fatalf("released %d, %v; want %d", n, err, releaseBatches)
	}
	if len(f.releases) != releaseBatches {
		t.Fatalf("%d release calls, want %d", len(f.releases), releaseBatches)
	}
	for i, r := range f.releases {
		if r.TenantID != tenant || r.CapabilityID != capability || r.AllTenants || r.Subject != "" || r.ClientID != "" ||
			r.Session != "" || r.OnBehalfOf != "" || r.RunID != uuid.Nil() || f.releasedIn[i] != tenant {
			t.Errorf("release %d = %+v acting on %s, want capability %s alone in tenant %s", i, r, f.releasedIn[i], capability, tenant)
		}
	}
	lines := logs.FilterMessage(msgCapabilityHoldsReleased).All()
	if len(lines) != 1 {
		t.Fatalf("%d lines, want 1", len(lines))
	}
	got := lines[0].ContextMap()
	if lines[0].Level != zapcore.InfoLevel || got[logKeyCapabilityID] != capability.String() || got[logKeyHoldsReleased] != int64(releaseBatches) {
		t.Errorf("logged %v %v, want info with capability %s and %d holds released", lines[0].Level, got, capability, releaseBatches)
	}
}

// A repeat that finds nothing left to release says so at debug.
func TestReleaseRevokedLogsARepeatAtDebug(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContext(context.Background(), zap.New(core))
	f := &fakeBulk{}
	if n, err := (&BulkRevocation{Revoker: f, Holds: f}).ReleaseRevoked(ctx, uuid.New(), uuid.New()); err != nil || n != 0 {
		t.Fatalf("released %d, %v; want none", n, err)
	}
	lines := logs.FilterMessage(msgCapabilityHoldsReleased).All()
	if len(lines) != 1 || lines[0].Level != zapcore.DebugLevel {
		t.Errorf("logged %v, want one line at debug", lines)
	}
}

// A failed release says what failed and logs no outcome: the caller answers
// it, and a retry releases what is left.
func TestReleaseRevokedFailure(t *testing.T) {
	errStore := errors.New("pgstore: connection refused")
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContext(context.Background(), zap.New(core))
	f := &fakeBulk{releaseErr: errStore}
	if _, err := (&BulkRevocation{Revoker: f, Holds: f}).ReleaseRevoked(ctx, uuid.New(), uuid.New()); !errors.Is(err, errStore) {
		t.Fatalf("err = %v, want the release's", err)
	}
	if logs.Len() != 0 {
		t.Errorf("logged %v, want nothing", logs.All())
	}
}

// With metering off, or the capability subsystem off, there is nothing to
// release.
func TestReleaseRevokedWithoutItsParts(t *testing.T) {
	f := &fakeBulk{}
	var off *BulkRevocation
	for name, b := range map[string]*BulkRevocation{"no holds": {Revoker: f}, "off": off} {
		if n, err := b.ReleaseRevoked(context.Background(), uuid.New(), uuid.New()); err != nil || n != 0 {
			t.Errorf("%s: %d, %v; want nothing", name, n, err)
		}
	}
	if len(f.releases) != 0 {
		t.Errorf("%d release calls, want none", len(f.releases))
	}
}

// An agent run's capabilities are revoked by the run, in its tenant, with the
// note and actor the operator gave, and their holds released by the same
// run; the line names the run.
func TestRevokeRunRevokesTheRunsCapabilities(t *testing.T) {
	tenant, run := uuid.New(), uuid.New()
	f := &fakeBulk{batches: 1}
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContext(context.Background(), zap.New(core))
	b := &BulkRevocation{Revoker: f, Holds: f}

	revoked, err := b.RevokeRun(ctx, tenant, run, "runaway agent", "user:operator")
	if err != nil {
		t.Fatal(err)
	}
	if revoked != 1 || len(f.revokeReqs) != 1 {
		t.Fatalf("revoked %d in %d calls, want 1 in 1", revoked, len(f.revokeReqs))
	}
	want := limes.RevokeMatchingRequest{TenantID: tenant, RunID: run, Note: "runaway agent", Actor: "user:operator"}
	if f.revokeReqs[0] != want {
		t.Errorf("revoke = %+v, want %+v", f.revokeReqs[0], want)
	}
	if len(f.releases) != 1 || f.releases[0].RunID != run || f.releases[0].TenantID != tenant {
		t.Errorf("releases = %+v, want the run's in its tenant", f.releases)
	}
	lines := logs.FilterMessage("capabilities revoked in bulk").All()
	if len(lines) != 1 || lines[0].ContextMap()[logKeyRunID] != run.String() {
		t.Errorf("logged %v, want the run %s named", lines, run)
	}
}
