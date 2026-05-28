package backendh

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
)

// stateBackendRepo is a stateful fake that records the enabled flag and
// can simulate an OCC version mismatch, so the SetBackendEnabled handler
// behaviour can be asserted without a database.
type stateBackendRepo struct {
	fakeBackendRepo
	enabled       bool
	rv            int64
	mismatch      bool // when true, SetEnabled returns ErrVersionMismatch
	notFound      bool // when true, SetEnabled returns ErrNotFound
	setEnabledHit int
}

func (r *stateBackendRepo) SetEnabled(_ context.Context, _ string, enabled bool, _ int64) error {
	r.setEnabledHit++
	if r.notFound {
		return admindomain.ErrNotFound
	}
	if r.mismatch {
		return admindomain.ErrVersionMismatch
	}
	r.enabled = enabled
	r.rv++
	return nil
}

func (r *stateBackendRepo) Get(_ context.Context, _ string) (admindomain.StorageBackend, error) {
	return admindomain.StorageBackend{
		BackendID: "primary", Kind: "s3-compatible",
		Enabled: r.enabled, ResourceVersion: r.rv,
	}, nil
}

func TestSetBackendEnabled_FlipsState(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1}
	h := NewHandler(repo, allowAuthorizer{}, "")

	out, err := h.SetBackendEnabled(ctxWithRoles("platform.admin"), "primary", false, 1)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if out.Enabled {
		t.Fatal("expected Enabled=false after disable")
	}
	if out.ResourceVersion <= 1 {
		t.Errorf("resource_version not advanced: %d", out.ResourceVersion)
	}
}

func TestSetBackendEnabled_IdempotentNoOp(t *testing.T) {
	repo := &stateBackendRepo{enabled: false, rv: 5}
	h := NewHandler(repo, allowAuthorizer{}, "")

	// Disabling an already-disabled backend succeeds (no-op semantics).
	out, err := h.SetBackendEnabled(ctxWithRoles("platform.admin"), "primary", false, 5)
	if err != nil {
		t.Fatalf("idempotent disable: %v", err)
	}
	if out.Enabled {
		t.Fatal("expected Enabled to stay false")
	}
}

func TestSetBackendEnabled_VersionMismatchAborts(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1, mismatch: true}
	h := NewHandler(repo, allowAuthorizer{}, "")

	_, err := h.SetBackendEnabled(ctxWithRoles("platform.admin"), "primary", false, 99)
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Errorf("stale version: got %v, want Aborted", connect.CodeOf(err))
	}
}

func TestSetBackendEnabled_NotFound(t *testing.T) {
	repo := &stateBackendRepo{notFound: true}
	h := NewHandler(repo, allowAuthorizer{}, "")

	_, err := h.SetBackendEnabled(ctxWithRoles("platform.admin"), "ghost", false, 1)
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("missing backend: got %v, want NotFound", connect.CodeOf(err))
	}
}

func TestSetBackendEnabled_DefaultBackendGuard(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1}
	// Construct the handler with "primary" as the configured default.
	h := NewHandler(repo, allowAuthorizer{}, "primary")

	// Disabling the default backend is refused (FR-006).
	_, err := h.SetBackendEnabled(ctxWithRoles("platform.admin"), "primary", false, 1)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("disable default: got %v, want FailedPrecondition", connect.CodeOf(err))
	}
	if repo.setEnabledHit != 0 {
		t.Error("repo.SetEnabled must not be reached when the default guard fires")
	}

	// Enabling the default backend is fine; disabling a NON-default is fine.
	if _, err := h.SetBackendEnabled(ctxWithRoles("platform.admin"), "primary", true, 1); err != nil {
		t.Errorf("enabling default should be allowed: %v", err)
	}
	if _, err := h.SetBackendEnabled(ctxWithRoles("platform.admin"), "secondary", false, 1); err != nil {
		t.Errorf("disabling non-default should be allowed: %v", err)
	}
}

func TestSetBackendEnabled_RequiresPlatformAdmin(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1}
	h := NewHandler(repo, allowAuthorizer{}, "")

	_, err := h.SetBackendEnabled(ctxWithRoles("tenant.admin"), "primary", false, 1)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("non-admin: got %v, want PermissionDenied", connect.CodeOf(err))
	}
	if repo.setEnabledHit != 0 {
		t.Error("repo.SetEnabled must not be reached when role check fails")
	}
}
