// Package backendh implements the admin BackendService — CRUD over storage
// backends. Mutations require role `platform.admin`. Reads are also allowed
// for `tenant.admin` and `bucket.admin` (so they can pick a target backend
// for new buckets / object_keys).
package backendh

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

const (
	rolePlatformAdmin = "platform.admin"
	roleBucketAdmin   = "bucket.admin"
	roleTenantAdmin   = "tenant.admin"
)

// Cedar action names — must match policies/schema.cedarschema. Centralised
// here so handler call-sites can't typo them silently.
const (
	actionManageBackend = "ManageBackend"
	actionReadBackend   = "ReadBackend"
)

type Handler struct {
	repo   admindomain.BackendRepository
	policy *cedar.Engine // optional; nil → role-only gating (legacy)
}

func NewHandler(r admindomain.BackendRepository, policyEngine *cedar.Engine) *Handler {
	return &Handler{repo: r, policy: policyEngine}
}

// authorize runs Cedar against the StorageBackend resource (`r.BackendID` is
// the natural key — backends are tenant-agnostic infra). Returns nil when
// the engine is unwired so legacy role-only deployments still work.
func (h *Handler) authorize(ctx context.Context, action, backendID string) error {
	if h.policy == nil {
		return nil
	}
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{Subject: p.Subject, TenantID: p.TenantID, Roles: p.Roles},
		action,
		&cedar.Resource{BackendID: backendID},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied,
			errors.New("denied by policy"))
	}
	return nil
}

// ─── Create ─────────────────────────────────────────────────────────────────

func (h *Handler) CreateBackend(ctx context.Context, b admindomain.StorageBackend) (*admindomain.StorageBackend, error) {
	if err := requireRole(ctx, rolePlatformAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, actionManageBackend, b.BackendID); err != nil {
		return nil, err
	}
	if b.BackendID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("backend_id required"))
	}
	if b.Kind == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("kind required"))
	}
	if err := h.repo.Upsert(ctx, b); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create backend: %w", err))
	}
	got, err := h.repo.Get(ctx, b.BackendID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &got, nil
}

// ─── Read ───────────────────────────────────────────────────────────────────

func (h *Handler) GetBackend(ctx context.Context, backendID string) (*admindomain.StorageBackend, error) {
	if err := requireAnyRole(ctx, rolePlatformAdmin, roleBucketAdmin, roleTenantAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, actionReadBackend, backendID); err != nil {
		return nil, err
	}
	b, err := h.repo.Get(ctx, backendID)
	if err != nil {
		if errors.Is(err, admindomain.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	// Tenant/bucket admins get a redacted view (no credentials_secret_ref).
	p, _ := auth.PrincipalFromContext(ctx)
	if !p.HasRole(rolePlatformAdmin) {
		b.CredentialsSecretRef = "[REDACTED]"
	}
	return &b, nil
}

func (h *Handler) ListBackends(ctx context.Context, pageSize int32, afterID string) ([]admindomain.StorageBackend, string, error) {
	if err := requireAnyRole(ctx, rolePlatformAdmin, roleBucketAdmin, roleTenantAdmin); err != nil {
		return nil, "", err
	}
	out, next, err := h.repo.List(ctx, pageSize, afterID)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInternal, err)
	}
	p, _ := auth.PrincipalFromContext(ctx)
	if !p.HasRole(rolePlatformAdmin) {
		for i := range out {
			out[i].CredentialsSecretRef = "[REDACTED]"
		}
	}
	return out, next, nil
}

// ─── Update ─────────────────────────────────────────────────────────────────

func (h *Handler) UpdateBackend(ctx context.Context, b admindomain.StorageBackend, expectedVersion int64, mask []string) (*admindomain.StorageBackend, error) {
	if err := requireRole(ctx, rolePlatformAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, actionManageBackend, b.BackendID); err != nil {
		return nil, err
	}
	if err := h.repo.Update(ctx, b, expectedVersion, mask); err != nil {
		if errors.Is(err, admindomain.ErrVersionMismatch) {
			return nil, connect.NewError(connect.CodeAborted, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	got, err := h.repo.Get(ctx, b.BackendID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &got, nil
}

func (h *Handler) RotateCredentials(ctx context.Context, backendID, secretRef string, _ time.Duration) (*admindomain.StorageBackend, error) {
	if err := requireRole(ctx, rolePlatformAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, actionManageBackend, backendID); err != nil {
		return nil, err
	}
	if err := h.repo.RotateCredentials(ctx, backendID, secretRef); err != nil {
		if errors.Is(err, admindomain.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	got, err := h.repo.Get(ctx, backendID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &got, nil
}

// ─── Delete ─────────────────────────────────────────────────────────────────

func (h *Handler) DeleteBackend(ctx context.Context, backendID string, expectedVersion int64, force bool) error {
	if err := requireRole(ctx, rolePlatformAdmin); err != nil {
		return err
	}
	if err := h.authorize(ctx, actionManageBackend, backendID); err != nil {
		return err
	}
	if err := h.repo.Delete(ctx, backendID, expectedVersion, force); err != nil {
		if errors.Is(err, admindomain.ErrVersionMismatch) {
			return connect.NewError(connect.CodeAborted, err)
		}
		if errors.Is(err, admindomain.ErrConflict) {
			return connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewError(connect.CodeInternal, err)
	}
	return nil
}

// ─── TestBackend ────────────────────────────────────────────────────────────

// TestBackendOutput is the result of a connectivity probe.
type TestBackendOutput struct {
	Reachable    bool
	ErrorMessage string
	LatencyMs    int32
}

func (h *Handler) TestBackend(ctx context.Context, backendID string) (*TestBackendOutput, error) {
	if err := requireRole(ctx, rolePlatformAdmin); err != nil {
		return nil, err
	}
	// Probe is wired by the storage adapter package in main.go; here we just
	// confirm the row exists. Real probe lives in `internal/storage/probe.go`
	// — wiring follows in slice 4.
	if _, err := h.repo.Get(ctx, backendID); err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return &TestBackendOutput{Reachable: true, LatencyMs: 0}, nil
}

// ─── role helpers ──────────────────────────────────────────────────────────

func requireRole(ctx context.Context, role string) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	if !p.HasRole(role) {
		return connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("role %q required", role))
	}
	return nil
}

func requireAnyRole(ctx context.Context, roles ...string) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	for _, r := range roles {
		if p.HasRole(r) {
			return nil
		}
	}
	return connect.NewError(connect.CodePermissionDenied, errors.New("insufficient role"))
}
