package backendh

import (
	"testing"

	"connectrpc.com/connect"
)

// SetBackendMaintenance (the schema baseline (001_initial_schema.sql)) mirrors SetBackendReadOnly's
// platform-admin + OCC + idempotency contract; advisory, and the default
// backend may be flagged.

func TestSetBackendMaintenance_FlipsState(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1}
	h := NewHandler(repo, allowAuthorizer{})

	out, err := h.SetBackendMaintenance(ctxWithRoles("platform.admin"), "primary", true, 1)
	if err != nil {
		t.Fatalf("set maintenance: %v", err)
	}
	if !out.Maintenance {
		t.Fatal("expected Maintenance=true")
	}
	if out.ResourceVersion <= 1 {
		t.Errorf("resource_version not advanced: %d", out.ResourceVersion)
	}
}

func TestSetBackendMaintenance_VersionMismatchAborts(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1, mismatch: true}
	h := NewHandler(repo, allowAuthorizer{})

	_, err := h.SetBackendMaintenance(ctxWithRoles("platform.admin"), "primary", true, 99)
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Errorf("stale version: got %v, want Aborted", connect.CodeOf(err))
	}
}

func TestSetBackendMaintenance_NotFound(t *testing.T) {
	repo := &stateBackendRepo{notFound: true}
	h := NewHandler(repo, allowAuthorizer{})

	_, err := h.SetBackendMaintenance(ctxWithRoles("platform.admin"), "ghost", true, 1)
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("missing backend: got %v, want NotFound", connect.CodeOf(err))
	}
}

// Flagging the configured default backend for maintenance is allowed (it's an
// advisory label, not a gate).
func TestSetBackendMaintenance_DefaultBackendAllowed(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1}
	h := NewHandler(repo, allowAuthorizer{}) // primary is the default

	if _, err := h.SetBackendMaintenance(ctxWithRoles("platform.admin"), "primary", true, 1); err != nil {
		t.Fatalf("maintenance on the default backend should be allowed: %v", err)
	}
	if repo.setMaintenanceHit != 1 {
		t.Errorf("repo.SetMaintenance hits = %d, want 1", repo.setMaintenanceHit)
	}
}

func TestSetBackendMaintenance_RequiresPlatformAdmin(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1}
	h := NewHandler(repo, allowAuthorizer{})

	_, err := h.SetBackendMaintenance(ctxWithRoles("tenant.admin"), "primary", true, 1)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("non-admin: got %v, want PermissionDenied", connect.CodeOf(err))
	}
	if repo.setMaintenanceHit != 0 {
		t.Error("repo.SetMaintenance must not be reached without platform.admin")
	}
}
