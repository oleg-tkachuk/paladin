package backendh

import (
	"testing"

	"connectrpc.com/connect"
)

// SetBackendReadOnly (drain, migration 047) mirrors SetBackendEnabled's
// platform-admin + OCC + idempotency contract, but with NO default-backend
// guard: draining the configured default is a legitimate migration step.

func TestSetBackendReadOnly_FlipsState(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1}
	h := NewHandler(repo, allowAuthorizer{}, "")

	out, err := h.SetBackendReadOnly(ctxWithRoles("platform.admin"), "primary", true, 1)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if !out.ReadOnly {
		t.Fatal("expected ReadOnly=true after drain")
	}
	if out.ResourceVersion <= 1 {
		t.Errorf("resource_version not advanced: %d", out.ResourceVersion)
	}
}

func TestSetBackendReadOnly_IdempotentNoOp(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, readOnly: true, rv: 5}
	h := NewHandler(repo, allowAuthorizer{}, "")

	out, err := h.SetBackendReadOnly(ctxWithRoles("platform.admin"), "primary", true, 5)
	if err != nil {
		t.Fatalf("idempotent drain: %v", err)
	}
	if !out.ReadOnly {
		t.Fatal("expected ReadOnly to stay true")
	}
}

func TestSetBackendReadOnly_VersionMismatchAborts(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1, mismatch: true}
	h := NewHandler(repo, allowAuthorizer{}, "")

	_, err := h.SetBackendReadOnly(ctxWithRoles("platform.admin"), "primary", true, 99)
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Errorf("stale version: got %v, want Aborted", connect.CodeOf(err))
	}
}

func TestSetBackendReadOnly_NotFound(t *testing.T) {
	repo := &stateBackendRepo{notFound: true}
	h := NewHandler(repo, allowAuthorizer{}, "")

	_, err := h.SetBackendReadOnly(ctxWithRoles("platform.admin"), "ghost", true, 1)
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("missing backend: got %v, want NotFound", connect.CodeOf(err))
	}
}

// Draining the configured default backend is allowed (unlike disabling it).
func TestSetBackendReadOnly_DefaultBackendAllowed(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1}
	h := NewHandler(repo, allowAuthorizer{}, "primary") // primary is the default

	out, err := h.SetBackendReadOnly(ctxWithRoles("platform.admin"), "primary", true, 1)
	if err != nil {
		t.Fatalf("draining the default backend should be allowed: %v", err)
	}
	if !out.ReadOnly {
		t.Fatal("expected ReadOnly=true on the default backend")
	}
	if repo.setReadOnlyHit != 1 {
		t.Errorf("repo.SetReadOnly hits = %d, want 1", repo.setReadOnlyHit)
	}
}

func TestSetBackendReadOnly_RequiresPlatformAdmin(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1}
	h := NewHandler(repo, allowAuthorizer{}, "")

	_, err := h.SetBackendReadOnly(ctxWithRoles("tenant.admin"), "primary", true, 1)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("non-admin: got %v, want PermissionDenied", connect.CodeOf(err))
	}
	if repo.setReadOnlyHit != 0 {
		t.Error("repo.SetReadOnly must not be reached without platform.admin")
	}
}
