package backendh

import (
	"errors"
	"strings"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/storage/features"
)

// A reachable backend is probed for its S3 features, and what the probe found
// is both returned and recorded (ADR-0026).
func TestTestBackend_ProbesAndRecordsFeatures(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1}
	found := []features.Result{{Feature: features.AnonymousReadPolicy, Support: features.Unsupported, Message: "no policies"}}
	h := NewHandler(repo, allowAuthorizer{})
	h.SetProber(&fakeProber{features: found})

	out, err := h.TestBackend(ctxWithRoles("platform.admin"), "primary")
	if err != nil {
		t.Fatalf("TestBackend: %v", err)
	}
	if len(out.Features) != 1 || out.Features[0] != found[0] {
		t.Errorf("returned %+v, want the probe's results", out.Features)
	}
	if repo.setFeaturesHit != 1 || len(repo.features) != 1 || repo.features[0] != found[0] {
		t.Errorf("recorded %d times: %+v; want the probe's results once", repo.setFeaturesHit, repo.features)
	}
}

// An unreachable backend is not feature-probed: there is nothing to learn,
// and the results recorded before stay as they were.
func TestTestBackend_UnreachableKeepsTheRecordedFeatures(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1}
	pr := &fakeProber{err: errors.New("dial tcp: connection refused")}
	h := NewHandler(repo, allowAuthorizer{})
	h.SetProber(pr)

	out, err := h.TestBackend(ctxWithRoles("platform.admin"), "primary")
	if err != nil {
		t.Fatalf("TestBackend: %v", err)
	}
	if pr.featuresCalled || out.Features != nil || repo.setFeaturesHit != 0 {
		t.Errorf("probed=%v returned=%v recorded=%d; want no feature probe", pr.featuresCalled, out.Features, repo.setFeaturesHit)
	}
}

// A backend no client can be built for gets every feature unknown, saying
// why, rather than an empty answer that reads as never probed.
func TestTestBackend_FeatureProbeErrorIsUnknownEverywhere(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1}
	h := NewHandler(repo, allowAuthorizer{})
	h.SetProber(&fakeProber{featureErr: errors.New("no secret resolver")})

	out, err := h.TestBackend(ctxWithRoles("platform.admin"), "primary")
	if err != nil {
		t.Fatalf("TestBackend: %v", err)
	}
	if len(out.Features) != len(features.Catalog) {
		t.Fatalf("%d results, want one per catalog feature", len(out.Features))
	}
	for _, r := range out.Features {
		if r.Support != features.Unknown || !strings.Contains(r.Message, "no secret resolver") || r.CheckedAt.IsZero() {
			t.Errorf("%s = %+v, want unknown carrying the error and when", r.Feature, r)
		}
	}
	if repo.setFeaturesHit != 1 {
		t.Errorf("recorded %d times, want once", repo.setFeaturesHit)
	}
}

// Recording is best-effort, as for health.
func TestTestBackend_FeatureWriteFailureIsBestEffort(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1, setFeaturesErr: errors.New("db down")}
	h := NewHandler(repo, allowAuthorizer{})
	h.SetProber(&fakeProber{features: features.EveryFeature(nil)})

	out, err := h.TestBackend(ctxWithRoles("platform.admin"), "primary")
	if err != nil || len(out.Features) != len(features.Catalog) {
		t.Errorf("TestBackend = %+v, %v; want the results despite the write failure", out, err)
	}
}
