package backendh

import (
	"errors"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/features"
)

// A new backend is probed once, as it is registered, so it does not show
// every feature unknown until an operator runs TestBackend (ADR-0026). The
// response carries what the probe recorded.
func TestCreateBackend_ProbesTheNewBackend(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1}
	pr := &fakeProber{features: features.EveryFeature(nil)}
	h := NewHandler(repo, allowAuthorizer{})
	h.SetProber(pr)

	out, err := h.CreateBackend(ctxWithRoles("platform.admin"), admindomain.StorageBackend{
		BackendID: "primary", Kind: "s3-compatible",
	})
	if err != nil {
		t.Fatalf("CreateBackend: %v", err)
	}
	if !pr.called || !pr.featuresCalled {
		t.Errorf("reachability probed=%v features probed=%v; want both", pr.called, pr.featuresCalled)
	}
	if repo.setHealthHit != 1 || repo.healthStatus != "ok" {
		t.Errorf("health recorded %d times as %q, want once as ok", repo.setHealthHit, repo.healthStatus)
	}
	if repo.setFeaturesHit != 1 || len(out.Features) != len(features.Catalog) {
		t.Errorf("features recorded %d times, returned %d; want once, one per catalog feature",
			repo.setFeaturesHit, len(out.Features))
	}
}

// An unreachable backend still registers: the probe's failure is recorded as
// its health, its features stay unknown, and the create succeeds.
func TestCreateBackend_UnreachableStillRegisters(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1}
	pr := &fakeProber{err: errors.New("dial tcp: connection refused")}
	h := NewHandler(repo, allowAuthorizer{})
	h.SetProber(pr)

	out, err := h.CreateBackend(ctxWithRoles("platform.admin"), admindomain.StorageBackend{
		BackendID: "primary", Kind: "s3-compatible",
	})
	if err != nil {
		t.Fatalf("CreateBackend: %v; want the backend registered", err)
	}
	if out.BackendID != "primary" {
		t.Errorf("got %q", out.BackendID)
	}
	if repo.healthStatus != "error" {
		t.Errorf("health = %q, want error", repo.healthStatus)
	}
	if pr.featuresCalled || repo.setFeaturesHit != 0 || len(out.Features) != 0 {
		t.Errorf("features probed=%v recorded=%d returned=%d; want none", pr.featuresCalled, repo.setFeaturesHit, len(out.Features))
	}
}

// Failing to record the probe does not fail the create either.
func TestCreateBackend_ProbeRecordingFailureIsBestEffort(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1,
		setHealthErr: errors.New("db down"), setFeaturesErr: errors.New("db down")}
	h := NewHandler(repo, allowAuthorizer{})
	h.SetProber(&fakeProber{featureErr: errors.New("no secret resolver")})

	if _, err := h.CreateBackend(ctxWithRoles("platform.admin"), admindomain.StorageBackend{
		BackendID: "primary", Kind: "s3-compatible",
	}); err != nil {
		t.Errorf("CreateBackend: %v; want success despite the probe", err)
	}
}

// A refused create probes nothing.
func TestCreateBackend_RefusedCreateIsNotProbed(t *testing.T) {
	pr := &fakeProber{}
	h := NewHandler(fakeBackendRepo{createErr: admindomain.ErrAlreadyExists}, allowAuthorizer{})
	h.SetProber(pr)

	if _, err := h.CreateBackend(ctxWithRoles("platform.admin"), admindomain.StorageBackend{
		BackendID: "primary", Kind: "s3-compatible",
	}); err == nil {
		t.Fatal("want the duplicate refused")
	}
	if pr.called {
		t.Error("a refused create was probed")
	}
}
