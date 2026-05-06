// Package tenant implements the TenantService business logic.
//
// Tenant creation is an admin-only operation. The AdminBucket action (with an
// empty objectKey) is reused as the "administrative" guard for tenant writes —
// production deployments can swap this for a dedicated platform-admin role
// check if Cedar gets a separate Tenant action.
package tenant

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// RolePlatformAdmin is the JWT role PALADIN treats as authorized to manage tenants.
// Kept in code (not Cedar) because tenant-creation decisions precede tenant
// entity existence — there's no Cedar principal hierarchy to evaluate yet.
const RolePlatformAdmin = "platform-admin"

type Tenant struct {
	TenantID             uuid.UUID
	DisplayName          string
	Labels               []byte // JSONB
	InheritedCedarPolicy string
	InheritedPolicyHash  []byte
	ResourceVersion      int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type CreateTenantArgs struct {
	TenantID             uuid.UUID
	DisplayName          string
	Labels               []byte
	InheritedCedarPolicy string
}

type UpdateTenantArgs struct {
	TenantID             uuid.UUID
	ExpectedVersion      int64
	DisplayName          *string
	Labels               []byte
	InheritedCedarPolicy *string
}

type Repository interface {
	Create(ctx context.Context, args CreateTenantArgs) (Tenant, error)
	Get(ctx context.Context, tenantID uuid.UUID) (Tenant, error)
	Update(ctx context.Context, args UpdateTenantArgs) (Tenant, error)
	Delete(ctx context.Context, tenantID uuid.UUID, expectedVersion int64) error
	List(ctx context.Context, pageSize int32, afterID uuid.UUID) ([]Tenant, string, error)
}

type Handler struct {
	repo   Repository
	policy cedar.Authorizer
}

// NewHandler builds a tenant handler. policyEngine is required — production
// wiring passes the live Cedar engine; tests inject a fake Authorizer.
func NewHandler(repo Repository, policyEngine cedar.Authorizer) *Handler {
	if policyEngine == nil {
		panic("tenant: policy authorizer is required")
	}
	return &Handler{repo: repo, policy: policyEngine}
}

// authorize evaluates Cedar against the Tenant resource.
func (h *Handler) authorize(ctx context.Context, action string, tenantID uuid.UUID) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{Subject: p.Subject, TenantID: p.TenantID, Roles: p.Roles},
		action,
		&cedar.Resource{TenantID: tenantID},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))
	}
	return nil
}

func (h *Handler) CreateTenant(ctx context.Context, args CreateTenantArgs) (*Tenant, error) {
	if err := requirePlatformAdmin(ctx); err != nil {
		return nil, err
	}
	if args.TenantID == uuid.Nil {
		args.TenantID = uuid.Must(uuid.NewV7())
	}
	if err := h.authorize(ctx, cedar.ActionManageTenant, args.TenantID); err != nil {
		return nil, err
	}
	// A tenant with no inherited policy would be deny-all at the Cedar layer
	// (empty policy set → no permit rule matches). Seed a sensible default so
	// newly-created tenants can immediately read/write their own objects.
	if args.InheritedCedarPolicy == "" {
		args.InheritedCedarPolicy = renderDefaultPolicy(args.TenantID)
	}
	t, err := h.repo.Create(ctx, args)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create tenant: %w", err))
	}
	return &t, nil
}

func (h *Handler) GetTenant(ctx context.Context, tenantID uuid.UUID) (*Tenant, error) {
	callerTenant, err := auth.TenantFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	// Non-admins may only view their own tenant.
	if callerTenant != tenantID {
		if err := requirePlatformAdmin(ctx); err != nil {
			return nil, err
		}
	}
	if err := h.authorize(ctx, cedar.ActionReadTenant, tenantID); err != nil {
		return nil, err
	}
	t, err := h.repo.Get(ctx, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return &t, nil
}

func (h *Handler) UpdateTenant(ctx context.Context, args UpdateTenantArgs) (*Tenant, error) {
	if err := requirePlatformAdmin(ctx); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionManageTenant, args.TenantID); err != nil {
		return nil, err
	}
	t, err := h.repo.Update(ctx, args)
	if err != nil {
		if errors.Is(err, ErrVersionMismatch) {
			return nil, connect.NewError(connect.CodeAborted, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &t, nil
}

func (h *Handler) DeleteTenant(ctx context.Context, tenantID uuid.UUID, expectedVersion int64) error {
	if err := requirePlatformAdmin(ctx); err != nil {
		return err
	}
	if err := h.authorize(ctx, cedar.ActionManageTenant, tenantID); err != nil {
		return err
	}
	if err := h.repo.Delete(ctx, tenantID, expectedVersion); err != nil {
		if errors.Is(err, ErrVersionMismatch) {
			return connect.NewError(connect.CodeAborted, err)
		}
		if errors.Is(err, ErrNotFound) {
			return connect.NewError(connect.CodeNotFound, err)
		}
		return connect.NewError(connect.CodeInternal, err)
	}
	return nil
}

func (h *Handler) ListTenants(ctx context.Context, pageSize int32, pageToken string) ([]Tenant, string, error) {
	if err := requirePlatformAdmin(ctx); err != nil {
		return nil, "", err
	}
	if err := h.authorize(ctx, cedar.ActionReadTenant, uuid.Nil); err != nil {
		return nil, "", err
	}
	var afterID uuid.UUID
	if pageToken != "" {
		id, err := uuid.Parse(pageToken)
		if err != nil {
			return nil, "", connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("invalid page_token: %w", err))
		}
		afterID = id
	}
	return h.repo.List(ctx, pageSize, afterID)
}

func requirePlatformAdmin(ctx context.Context) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	if !p.HasRole(RolePlatformAdmin) {
		return connect.NewError(connect.CodePermissionDenied,
			errors.New("platform-admin role required"))
	}
	_ = cedar.ActionAdminObjectKey // reserved for tenant-level Cedar rollout
	return nil
}

// ErrVersionMismatch — OCC failure surfaced by Repository.
var ErrVersionMismatch = errors.New("resource_version mismatch")

// ErrNotFound — Repository returns this when the target row does not exist
// and the operation did not use an OCC guard (so a 0-rows result is an
// unambiguous "missing", not a version conflict).
var ErrNotFound = errors.New("tenant not found")
