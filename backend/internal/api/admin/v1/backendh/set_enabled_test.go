package backendh

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
)

// stateBackendRepo is a stateful fake that records the enabled flag and
// can simulate an OCC version mismatch, so the SetBackendEnabled handler
// behaviour can be asserted without a database.
type stateBackendRepo struct {
	fakeBackendRepo
	enabled           bool
	readOnly          bool
	rv                int64
	mismatch          bool // when true, SetEnabled/SetReadOnly returns ErrVersionMismatch
	notFound          bool // when true, SetEnabled/SetReadOnly returns ErrNotFound
	setEnabledHit     int
	setReadOnlyHit    int
	maintenance       bool
	setMaintenanceHit int
	// Health probe recording (migration 048).
	healthStatus  string
	healthMessage string
	setHealthHit  int
	setHealthErr  error // when set, SetHealth returns it (best-effort path)
}

func (r *stateBackendRepo) SetHealth(_ context.Context, _ string, status, message string, _ time.Time) error {
	r.setHealthHit++
	if r.setHealthErr != nil {
		return r.setHealthErr
	}
	r.healthStatus = status
	r.healthMessage = message
	return nil
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

func (r *stateBackendRepo) SetReadOnly(_ context.Context, _ string, readOnly bool, _ int64) error {
	r.setReadOnlyHit++
	if r.notFound {
		return admindomain.ErrNotFound
	}
	if r.mismatch {
		return admindomain.ErrVersionMismatch
	}
	r.readOnly = readOnly
	r.rv++
	return nil
}

func (r *stateBackendRepo) SetMaintenance(_ context.Context, _ string, maintenance bool, _ int64) error {
	r.setMaintenanceHit++
	if r.notFound {
		return admindomain.ErrNotFound
	}
	if r.mismatch {
		return admindomain.ErrVersionMismatch
	}
	r.maintenance = maintenance
	r.rv++
	return nil
}

func (r *stateBackendRepo) Get(_ context.Context, _ string) (admindomain.StorageBackend, error) {
	return admindomain.StorageBackend{
		BackendID: "primary", Kind: "s3-compatible",
		Enabled: r.enabled, ReadOnly: r.readOnly, Maintenance: r.maintenance,
		ResourceVersion: r.rv,
	}, nil
}

func TestSetBackendEnabled_FlipsState(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1}
	h := NewHandler(repo, allowAuthorizer{})

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
	h := NewHandler(repo, allowAuthorizer{})

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
	h := NewHandler(repo, allowAuthorizer{})

	_, err := h.SetBackendEnabled(ctxWithRoles("platform.admin"), "primary", false, 99)
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Errorf("stale version: got %v, want Aborted", connect.CodeOf(err))
	}
}

func TestSetBackendEnabled_NotFound(t *testing.T) {
	repo := &stateBackendRepo{notFound: true}
	h := NewHandler(repo, allowAuthorizer{})

	_, err := h.SetBackendEnabled(ctxWithRoles("platform.admin"), "ghost", false, 1)
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("missing backend: got %v, want NotFound", connect.CodeOf(err))
	}
}

// There is no default backend anymore, so there is no default-backend disable
// guard: ANY backend can be disabled (the operator drains first if it holds
// buckets, and operations to a disabled backend fail loudly).
func TestSetBackendEnabled_NoDefaultGuard(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1}
	h := NewHandler(repo, allowAuthorizer{})

	if _, err := h.SetBackendEnabled(ctxWithRoles("platform.admin"), "primary", false, 1); err != nil {
		t.Errorf("disabling any backend should be allowed (no default guard): %v", err)
	}
	if repo.setEnabledHit == 0 {
		t.Error("repo.SetEnabled must be reached — no guard should short-circuit it")
	}
}

func TestSetBackendEnabled_RequiresPlatformAdmin(t *testing.T) {
	repo := &stateBackendRepo{enabled: true, rv: 1}
	h := NewHandler(repo, allowAuthorizer{})

	_, err := h.SetBackendEnabled(ctxWithRoles("tenant.admin"), "primary", false, 1)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("non-admin: got %v, want PermissionDenied", connect.CodeOf(err))
	}
	if repo.setEnabledHit != 0 {
		t.Error("repo.SetEnabled must not be reached when role check fails")
	}
}
