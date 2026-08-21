// Package eventsubh implements the admin EventSubscriptionService.
package eventsubh

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	celpkg "github.com/oleg-tkachuk/paladin/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

type Handler struct {
	repo       admindomain.EventSubscriptionRepository
	dispatcher Dispatcher
	policy     cedar.Authorizer
}

func NewHandler(r admindomain.EventSubscriptionRepository, policy cedar.Authorizer) *Handler {
	if policy == nil {
		panic("eventsubh: policy authorizer is required")
	}
	return &Handler{repo: r, policy: policy}
}

// authorize gates a subscription RPC against Cedar and, on success, returns
// a context scoped to the tenant that was authorised. Returning the context
// rather than just an error is deliberate: the RLS pool reads its tenant
// from there, so authorising a cross-tenant action and being able to perform
// it become one step that cannot be half-done.
//
// existing role + tenant-isolation guards stay as defense-in-depth.
func (h *Handler) authorize(ctx context.Context, action string, tenantID uuid.UUID) (context.Context, error) {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return ctx, connect.NewError(connect.CodeUnauthenticated, err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(p),
		action,
		&cedar.Resource{TenantID: tenantID},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return ctx, connect.NewError(connect.CodeInternal, fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return ctx, connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))
	}
	return auth.WithActingTenant(ctx, tenantID), nil
}

func (h *Handler) Create(ctx context.Context, s admindomain.EventSubscription) (*admindomain.EventSubscription, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := apiutil.RequireAnyRole(ctx, apiutil.RolePlatformAdmin, apiutil.RoleTenantAdmin); err != nil {
		return nil, err
	}
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) && s.TenantID != caller {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("cross-tenant denied"))
	}
	if ctx, err = h.authorize(ctx, cedar.ActionManageSubscription, s.TenantID); err != nil {
		return nil, err
	}
	// Reject a malformed CEL filter synchronously — otherwise the dispatcher
	// fails it closed at fan-out time and the operator sees silent
	// non-delivery hours later (see worker.Dispatcher.subscriptionMatches).
	if err := celpkg.Validate(celpkg.EventEnvelopeSchema, s.CELFilter); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("filter: %w", err))
	}
	if err := h.repo.Create(ctx, &s); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	// repo.Create stamps the generated SubscriptionID onto s; without
	// the pointer receiver this Get would look up the caller's zero
	// UUID and fail closed with ErrNotFound, leaving an "orphan" row
	// in the DB and surfacing a confusing "admin: resource not found"
	// CodeInternal back to the operator.
	got, err := h.repo.Get(ctx, s.SubscriptionID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &got, nil
}

// Get takes the tenant from the resource name rather than discovering it
// from the row. The row cannot be read first: event_subscriptions is
// RLS-isolated, so a read before the scope is set returns nothing for any
// tenant but the caller's — which would surface as "not found" for a
// platform admin looking at a subscription that plainly exists.
func (h *Handler) Get(ctx context.Context, tenantID, id uuid.UUID) (*admindomain.EventSubscription, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) && tenantID != caller {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("subscription not found"))
	}
	if ctx, err = h.authorize(ctx, cedar.ActionReadSubscription, tenantID); err != nil {
		return nil, err
	}
	s, err := h.repo.Get(ctx, id)
	if err != nil {
		return nil, apiutil.MapError(err)
	}
	return &s, nil
}

func (h *Handler) Update(ctx context.Context, tenantID uuid.UUID, s admindomain.EventSubscription, expectedVersion int64, mask []string) (*admindomain.EventSubscription, error) {
	current, err := h.Get(ctx, tenantID, s.SubscriptionID)
	if err != nil {
		return nil, err
	}
	if ctx, err = h.authorize(ctx, cedar.ActionManageSubscription, current.TenantID); err != nil {
		return nil, err
	}
	// Validate the CEL filter only when this update actually writes it —
	// an empty mask means full replace (AIP), otherwise the "filter" path
	// must be present. Skips false-rejecting a stale filter the caller isn't
	// applying (e.g. a sink-only update).
	if len(mask) == 0 || slices.Contains(mask, "filter") {
		if err := celpkg.Validate(celpkg.EventEnvelopeSchema, s.CELFilter); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("filter: %w", err))
		}
	}
	if err := h.repo.Update(ctx, s, expectedVersion, mask); err != nil {
		return nil, apiutil.MapError(err)
	}
	got, _ := h.repo.Get(ctx, s.SubscriptionID)
	return &got, nil
}

func (h *Handler) Delete(ctx context.Context, tenantID, id uuid.UUID, expectedVersion int64) error {
	current, err := h.Get(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if ctx, err = h.authorize(ctx, cedar.ActionManageSubscription, current.TenantID); err != nil {
		return err
	}
	if err := h.repo.Delete(ctx, id, expectedVersion); err != nil {
		return apiutil.MapError(err)
	}
	return nil
}

func (h *Handler) List(ctx context.Context, args admindomain.ListEventSubscriptionsArgs) ([]admindomain.EventSubscription, string, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, "", err
	}
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) {
		args.TenantID = caller
	}
	if ctx, err = h.authorize(ctx, cedar.ActionReadSubscription, args.TenantID); err != nil {
		return nil, "", err
	}
	return h.repo.List(ctx, args)
}

// Dispatcher delivers a synthetic event to the subscription's sink for
// connectivity / signature verification testing. Optional — when nil,
// TestSubscription returns Unimplemented.
type Dispatcher interface {
	DeliverOne(ctx context.Context, sub admindomain.EventSubscription, eventType string) error
}

// SetDispatcher attaches the optional dispatcher used by TestSubscription.
// Wired in cmd/server/root.go from internal/worker.Dispatcher.
func (h *Handler) SetDispatcher(d Dispatcher) { h.dispatcher = d }

// TestSubscription delivers a synthetic event ("paladin.test") to the sink.
// Returns the delivery attempt's status as the connect-level error so the
// admin UI surfaces it directly to the operator.
func (h *Handler) TestSubscription(ctx context.Context, tenantID, id uuid.UUID) error {
	sub, err := h.Get(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if ctx, err = h.authorize(ctx, cedar.ActionTestSubscription, sub.TenantID); err != nil {
		return err
	}
	if h.dispatcher == nil {
		return connect.NewError(connect.CodeUnimplemented,
			errors.New("event dispatcher not wired; TestSubscription unavailable"))
	}
	if err := h.dispatcher.DeliverOne(ctx, *sub, "paladin.test"); err != nil {
		return connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("test delivery failed: %w", err))
	}
	return nil
}
