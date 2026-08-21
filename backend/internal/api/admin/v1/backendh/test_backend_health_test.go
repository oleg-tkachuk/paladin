package backendh

import (
	"errors"
	"testing"
)

// TestBackend records the probe outcome as the backend's derived health
// (the schema baseline (001_initial_schema.sql)): ok on success, error (+message) on failure. (fakeProber
// lives in probe_test.go.)

func TestTestBackend_RecordsHealthOK(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1}
	h := NewHandler(repo, allowAuthorizer{})
	h.SetProber(&fakeProber{err: nil})

	out, err := h.TestBackend(ctxWithRoles("platform.admin"), "primary")
	if err != nil {
		t.Fatalf("TestBackend: %v", err)
	}
	if !out.Reachable {
		t.Fatal("expected Reachable=true")
	}
	if repo.setHealthHit != 1 || repo.healthStatus != "ok" || repo.healthMessage != "" {
		t.Fatalf("health = %d/%q/%q, want 1/ok/empty", repo.setHealthHit, repo.healthStatus, repo.healthMessage)
	}
}

func TestTestBackend_RecordsHealthError(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1}
	h := NewHandler(repo, allowAuthorizer{})
	h.SetProber(&fakeProber{err: errors.New("dial tcp: connection refused")})

	out, err := h.TestBackend(ctxWithRoles("platform.admin"), "primary")
	if err != nil {
		t.Fatalf("TestBackend: %v", err)
	}
	if out.Reachable {
		t.Fatal("expected Reachable=false")
	}
	if repo.setHealthHit != 1 || repo.healthStatus != "error" {
		t.Fatalf("health = %d/%q, want 1/error", repo.setHealthHit, repo.healthStatus)
	}
	if repo.healthMessage == "" {
		t.Error("expected the probe error to be recorded as health_message")
	}
}

// A health-write failure is best-effort: it must not fail the probe response.
func TestTestBackend_HealthWriteFailureIsBestEffort(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1, setHealthErr: errors.New("db down")}
	h := NewHandler(repo, allowAuthorizer{})
	h.SetProber(&fakeProber{err: nil})

	out, err := h.TestBackend(ctxWithRoles("platform.admin"), "primary")
	if err != nil {
		t.Fatalf("TestBackend must not fail on a health-write error: %v", err)
	}
	if !out.Reachable {
		t.Fatal("expected Reachable=true despite the health-write failure")
	}
}

// With no prober wired, TestBackend reports unreachable and does NOT record
// health (there was no probe to record).
func TestTestBackend_NoProberDoesNotRecordHealth(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1}
	h := NewHandler(repo, allowAuthorizer{})

	out, err := h.TestBackend(ctxWithRoles("platform.admin"), "primary")
	if err != nil {
		t.Fatalf("TestBackend: %v", err)
	}
	if out.Reachable {
		t.Fatal("expected Reachable=false with no prober")
	}
	if repo.setHealthHit != 0 {
		t.Errorf("SetHealth hits = %d, want 0 when no prober is wired", repo.setHealthHit)
	}
}
