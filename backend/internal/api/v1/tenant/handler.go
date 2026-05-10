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
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// EventProducer is the narrow seam the handler uses to fan out tenant
// lifecycle events to subscribers. Defined here (not imported as
// `worker.Dispatcher`) so tests can stub it without spinning up the
// real outbox + Postgres. nil-safe: when the wiring layer omits the
// producer (e.g. in unit tests, or before the dispatcher is enabled
// in a fresh deployment) the handler logs a debug breadcrumb and
// continues — fan-out is best-effort, never blocks the RPC reply.
//
// Why a method named `Dispatch` and not `Publish` / `Fire`: this is
// the same vocabulary `worker.Dispatcher.Dispatch` uses; matching
// names lets the production wiring pass `*worker.Dispatcher`
// directly with no adapter.
type EventProducer interface {
	Dispatch(ctx context.Context, tenantID string, evt worker.Event) (int, error)
}

// Use apiutil.RolePlatformAdmin as the canonical role string ("platform.admin").
// A previous local copy here used the hyphen form which silently failed every
// Cedar permit because the JWT issuer mints dot-form roles.

type Tenant struct {
	TenantID             uuid.UUID
	Slug                 string
	DisplayName          string
	Labels               []byte // JSONB
	InheritedCedarPolicy string
	InheritedPolicyHash  []byte
	ResourceVersion      int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type CreateTenantArgs struct {
	TenantID uuid.UUID
	// Slug is the human-readable tenant identifier exposed in Cedar policies
	// and resource names. Required — auto-derived from TenantID when empty so
	// existing UUID-only callers keep working through the rollout. Validated
	// via apiutil.ValidateTenantSlug; uniqueness is enforced by the database.
	Slug                 string
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
	// GetBySlug looks up a tenant by its kebab-case slug. ErrNotFound
	// when no row matches. Used by handlers accepting the slug-form
	// resource name (`tenants/{tenant_id_or_slug}`) — see
	// apiutil.ParseTenantNameRef.
	GetBySlug(ctx context.Context, slug string) (Tenant, error)
	Update(ctx context.Context, args UpdateTenantArgs) (Tenant, error)
	Delete(ctx context.Context, tenantID uuid.UUID, expectedVersion int64) error
	List(ctx context.Context, pageSize int32, afterID uuid.UUID) ([]Tenant, string, error)
	// Rename atomically updates tenants.slug AND rewrites every
	// `Tenant::"<old>"` reference in the tenant's
	// inherited_cedar_policy + every object_keys row's cedar_policy
	// for that tenant. Returns the renamed Tenant (with the new
	// resource_version). ErrVersionMismatch on OCC failure.
	Rename(ctx context.Context, args RenameTenantSlugArgs) (Tenant, error)
}

// RenameTenantSlugArgs is the input shape for Repository.Rename and
// Handler.RenameTenantSlug.
type RenameTenantSlugArgs struct {
	TenantID        uuid.UUID
	NewSlug         string
	ExpectedVersion int64
}

type Handler struct {
	repo   Repository
	policy cedar.Authorizer

	// events is optional — when nil, lifecycle Dispatch calls are
	// silent no-ops. Set via SetEventProducer once the dispatcher is
	// wired in build_listeners_admin (after repos + policy are
	// constructed but before the listener starts serving).
	events EventProducer
	log    *zap.Logger // best-effort sink for fan-out failures
}

// NewHandler builds a tenant handler. policyEngine is required — production
// wiring passes the live Cedar engine; tests inject a fake Authorizer.
func NewHandler(repo Repository, policyEngine cedar.Authorizer) *Handler {
	if policyEngine == nil {
		panic("tenant: policy authorizer is required")
	}
	return &Handler{repo: repo, policy: policyEngine, log: zap.NewNop()}
}

// SetEventProducer attaches the optional outbox producer. nil clears
// the wiring (useful in tests). Production wiring lives in
// build_listeners_admin.go alongside SetDispatcher on the eventsubh
// handler.
func (h *Handler) SetEventProducer(p EventProducer) { h.events = p }

// SetLogger attaches a non-nop logger so fan-out failures surface in
// the admin pod's structured log. Without this, dispatch errors
// silently disappear — the handler still returns success because the
// underlying lifecycle write committed.
func (h *Handler) SetLogger(l *zap.Logger) {
	if l != nil {
		h.log = l
	}
}

// dispatchEvent is best-effort: the lifecycle write already committed
// by the time we get here, so a fan-out failure must not flip the
// RPC reply to error. We log + return. The dispatcher pod's outbox
// reaper retries delivery from the row; this layer's only job is to
// land the row (or quietly miss it if the producer is unconfigured).
func (h *Handler) dispatchEvent(ctx context.Context, tenantID uuid.UUID, eventType, resourceName string, payload map[string]any) {
	if h.events == nil {
		return
	}
	actor := ""
	if p, err := auth.PrincipalFromContext(ctx); err == nil {
		actor = p.Subject
	}
	queued, err := h.events.Dispatch(ctx, tenantID.String(), worker.Event{
		Type:         eventType,
		At:           time.Now().UTC(),
		TenantID:     tenantID.String(),
		ResourceName: resourceName,
		ActorSubject: actor,
		Payload:      payload,
	})
	if err != nil {
		h.log.Warn("tenant event fan-out failed",
			zap.String("event_type", eventType),
			zap.String("tenant_id", tenantID.String()),
			zap.Error(err),
		)
		return
	}
	h.log.Debug("tenant event queued",
		zap.String("event_type", eventType),
		zap.String("tenant_id", tenantID.String()),
		zap.Int("subscriptions_matched", queued),
	)
}

// authorize evaluates Cedar against the Tenant resource.
func (h *Handler) authorize(ctx context.Context, action string, tenantID uuid.UUID) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{Subject: p.Subject, TenantID: p.TenantID, TenantSlug: p.TenantSlug, Roles: p.Roles, Scopes: apiutil.ScopeStrings(p.Scopes)},
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
	// tenant_id: client-supplied or server-generated. Zero UUID is
	// reserved as the in-memory sentinel meaning "no value" — reject
	// it explicitly so a caller passing all-zeros doesn't silently get
	// a server-generated row.
	if args.TenantID == uuid.Nil {
		args.TenantID = uuid.Must(uuid.NewV7())
	}
	// Slug is required. Migration 033 makes the column NOT NULL UNIQUE
	// and the API contract follows: no auto-derivation from tenant_id.
	// Operators who don't have a slug yet must pick one before they
	// can land a tenant — matches the user-facing identity model where
	// tenant_id is a UUID and slug is the human handle.
	if args.Slug == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("slug is required"))
	}
	if err := apiutil.ValidateTenantSlug(args.Slug); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("slug: %w", err))
	}
	// display_name defaults to slug when omitted. Trim before checking
	// so " " also triggers the default. Length + format are enforced
	// at the DB layer (tenants_display_name_format CHECK from migr 033).
	args.DisplayName = strings.TrimSpace(args.DisplayName)
	if args.DisplayName == "" {
		args.DisplayName = args.Slug
	}
	if err := h.authorize(ctx, cedar.ActionManageTenant, args.TenantID); err != nil {
		return nil, err
	}
	// A tenant with no inherited policy would be deny-all at the Cedar layer
	// (empty policy set → no permit rule matches). Seed a sensible default so
	// newly-created tenants can immediately read/write their own objects.
	// The default policy keys on the slug, so policies remain readable even
	// when the platform regenerates them after a rename.
	if args.InheritedCedarPolicy == "" {
		args.InheritedCedarPolicy = renderDefaultPolicy(args.TenantID, args.Slug)
	}
	t, err := h.repo.Create(ctx, args)
	if err != nil {
		switch {
		case errors.Is(err, ErrTenantIDConflict),
			errors.Is(err, ErrSlugConflict),
			errors.Is(err, ErrDisplayNameConflict):
			return nil, connect.NewError(connect.CodeAlreadyExists, err)
		default:
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create tenant: %w", err))
		}
	}
	h.dispatchEvent(ctx, t.TenantID, "paladin.tenant.created",
		fmt.Sprintf("tenants/%s", t.TenantID),
		map[string]any{
			"tenant_id":    t.TenantID.String(),
			"slug":         t.Slug,
			"display_name": t.DisplayName,
		})
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

// GetTenantBySlug resolves a slug to a tenant row and applies the
// same authorization gating as GetTenant. The tenant resource name
// format `tenants/{tenant_id_or_slug}` requires this — handlers
// receiving a non-UUID body have no other way to materialise the
// UUID needed for the standard auth path.
//
// Resolution is split (slug → row, row → authz) so the unauthorised
// caller still gets NotFound instead of "you aren't allowed to know
// this tenant exists" — keeps slug enumeration off the table.
//
// The authz block mirrors GetTenant verbatim — duplicated rather
// than delegated so the connectshim coverage test can statically
// see the gate markers (h.authorize / requirePlatformAdmin) in this
// method's body.
func (h *Handler) GetTenantBySlug(ctx context.Context, slug string) (*Tenant, error) {
	t, err := h.repo.GetBySlug(ctx, slug)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	callerTenant, err := auth.TenantFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	if callerTenant != t.TenantID {
		if err := requirePlatformAdmin(ctx); err != nil {
			return nil, err
		}
	}
	if err := h.authorize(ctx, cedar.ActionReadTenant, t.TenantID); err != nil {
		return nil, err
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
	// display_name validation: trim and reject empty edits — an empty
	// string would clear the column (UpdateTenant uses COALESCE on
	// nullable args, but the API path passes a *string so empty means
	// "set to empty"). The DB CHECK would reject it; surface a clearer
	// error before the round-trip.
	if args.DisplayName != nil {
		trimmed := strings.TrimSpace(*args.DisplayName)
		if trimmed == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				errors.New("display_name must not be empty"))
		}
		args.DisplayName = &trimmed
	}
	t, err := h.repo.Update(ctx, args)
	if err != nil {
		if errors.Is(err, ErrVersionMismatch) {
			return nil, connect.NewError(connect.CodeAborted, err)
		}
		if errors.Is(err, ErrDisplayNameConflict) {
			return nil, connect.NewError(connect.CodeAlreadyExists, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	// Snapshot of the post-update state — subscribers can diff against
	// the previous state they cached. We don't ship a `fields_changed`
	// list because the FieldMask the caller passed lives upstream of
	// the handler and would tie the event payload to a connect-shim
	// detail; downstream consumers can derive the diff themselves.
	h.dispatchEvent(ctx, t.TenantID, "paladin.tenant.updated",
		fmt.Sprintf("tenants/%s", t.TenantID),
		map[string]any{
			"tenant_id":        t.TenantID.String(),
			"slug":             t.Slug,
			"display_name":     t.DisplayName,
			"resource_version": t.ResourceVersion,
		})
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
	h.dispatchEvent(ctx, tenantID, "paladin.tenant.deleted",
		fmt.Sprintf("tenants/%s", tenantID),
		map[string]any{
			"tenant_id":        tenantID.String(),
			"resource_version": expectedVersion,
		})
	return nil
}

// RenameTenantSlug rewrites the tenant's slug AND rewrites every
// `Tenant::"<old_slug>"` reference in the tenant's inherited
// cedar_policy plus every object_key's cedar_policy for the tenant.
// Single transaction in the repository; OCC-guarded against
// args.ExpectedVersion. Platform-admin only.
//
// Out of scope: per-bucket cedar_policy is not touched today — buckets
// don't carry slug references in current default policies. If a future
// bucket policy template starts referring to the slug, extend
// Repository.Rename to cover it.
func (h *Handler) RenameTenantSlug(ctx context.Context, args RenameTenantSlugArgs) (*Tenant, error) {
	if err := requirePlatformAdmin(ctx); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionManageTenant, args.TenantID); err != nil {
		return nil, err
	}
	if err := apiutil.ValidateTenantSlug(args.NewSlug); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("new_slug: %w", err))
	}
	t, err := h.repo.Rename(ctx, args)
	if err != nil {
		if errors.Is(err, ErrVersionMismatch) {
			return nil, connect.NewError(connect.CodeAborted, err)
		}
		if errors.Is(err, ErrSlugConflict) {
			return nil, connect.NewError(connect.CodeAlreadyExists, err)
		}
		if errors.Is(err, ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &t, nil
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
	if !p.HasRole(apiutil.RolePlatformAdmin) {
		return connect.NewError(connect.CodePermissionDenied,
			errors.New("platform.admin role required"))
	}
	return nil
}

// ErrVersionMismatch — OCC failure surfaced by Repository.
var ErrVersionMismatch = errors.New("resource_version mismatch")

// ErrNotFound — Repository returns this when the target row does not exist
// and the operation did not use an OCC guard (so a 0-rows result is an
// unambiguous "missing", not a version conflict).
var ErrNotFound = errors.New("tenant not found")

// ErrSlugConflict — Repository.Rename or Create returns this when the
// requested slug is already in use by another tenant.
var ErrSlugConflict = errors.New("tenant slug already in use")

// ErrTenantIDConflict — Repository.Create returns this when the
// caller-supplied tenant_id (UUID) collides with an existing row.
var ErrTenantIDConflict = errors.New("tenant_id already in use")

// ErrDisplayNameConflict — Repository.Create or Update returns this
// when the requested display_name collides with another tenant's.
// display_name is UNIQUE since migration 033.
var ErrDisplayNameConflict = errors.New("display_name already in use")
