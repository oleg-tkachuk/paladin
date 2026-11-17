// Package eventsubh implements the admin EventSubscriptionService.
package eventsubh

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/eventtype"
	celpkg "github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
)

type Handler struct {
	repo       admindomain.EventSubscriptionRepository
	dispatcher Dispatcher
	policy     cedar.Authorizer
	sinks      SinkKinds
	log        *zap.Logger
}

// SetLogger sets where the handler says how each change ended. Unset, it
// says nothing.
func (h *Handler) SetLogger(l *zap.Logger) { h.log = l }

// subFields names a subscription in a log line.
func subFields(s admindomain.EventSubscription) []zap.Field {
	return []zap.Field{
		zap.String("tenant_id", s.TenantID.String()),
		zap.String("subscription_id", s.SubscriptionID.String()),
		zap.String("sink_kind", s.SinkKind),
	}
}

// failed logs a store failure on s and maps it for the caller: refusals the
// caller can act on keep their code, and anything internal is an error line.
func (h *Handler) failed(msg string, s admindomain.EventSubscription, err error) error {
	mapped := apiutil.MapError(err)
	level := zapcore.WarnLevel
	if connect.CodeOf(mapped) == connect.CodeInternal {
		level = zapcore.ErrorLevel
	}
	h.log.Log(level, msg, append(subFields(s), zap.Error(err))...)
	return mapped
}

// SinkKinds says which sink kinds the dispatcher delivers to
// (config.DispatcherSinks).
type SinkKinds interface {
	Enabled(kind string) bool
}

// SetSinkKinds attaches the sink-kind switches. Unset, every kind is on.
func (h *Handler) SetSinkKinds(k SinkKinds) { h.sinks = k }

// refuseOffKind refuses a subscription that would deliver to a kind switched
// off: the dispatcher would fail every one of its deliveries. A disabled
// subscription delivers nothing, so it may name any kind.
func (h *Handler) refuseOffKind(s admindomain.EventSubscription, kind string, disabled bool) error {
	if disabled || h.sinks == nil || h.sinks.Enabled(kind) {
		return nil
	}
	h.log.Warn("event subscription refused: sink kind is off",
		append(subFields(s), zap.String("switch", config.SinkSwitchKey(kind)))...)
	return connect.NewError(connect.CodeFailedPrecondition,
		fmt.Sprintf("sink kind %s is off by configuration: %s", kind, config.SinkSwitchKey(kind)))
}

func NewHandler(r admindomain.EventSubscriptionRepository, policy cedar.Authorizer) *Handler {
	if policy == nil {
		panic("eventsubh: policy authorizer is required")
	}
	return &Handler{repo: r, policy: policy, log: zap.NewNop()}
}

// authorize gates a subscription RPC against Cedar and, on success, returns
// a context scoped to the tenant that was authorised. Returning the context
// rather than just an error is deliberate: the RLS pool reads its tenant
// from there, so authorising a cross-tenant action and being able to perform
// it become one step that cannot be half-done.
//
// existing role + tenant-isolation guards stay as defense-in-depth.
func (h *Handler) authorize(ctx context.Context, action cedar.Action, tenantID uuid.UUID) (context.Context, error) {
	if _, err := apiutil.Authorize(ctx, h.policy, action, &cedar.Resource{TenantID: tenantID}); err != nil {
		return ctx, err
	}
	return auth.WithActingTenant(ctx, tenantID), nil
}

// requireCreateRole admits a platform admin to create a subscription for
// any tenant, and a tenant admin for its own.
func requireCreateRole(ctx context.Context, tenantID uuid.UUID) error {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return err
	}
	if err := apiutil.RequireAnyRole(ctx, apiutil.RolePlatformAdmin, apiutil.RoleTenantAdmin); err != nil {
		return err
	}
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) && tenantID != caller {
		return connect.NewError(connect.CodePermissionDenied, "cross-tenant denied")
	}
	return nil
}

func (h *Handler) Create(ctx context.Context, s admindomain.EventSubscription) (*admindomain.EventSubscription, error) {
	if err := requireCreateRole(ctx, s.TenantID); err != nil {
		return nil, err
	}
	ctx, err := h.authorize(ctx, cedar.ActionManageSubscription, s.TenantID)
	if err != nil {
		return nil, err
	}
	// Reject a malformed CEL filter synchronously — otherwise the dispatcher
	// fails it closed at fan-out time and the operator sees silent
	// non-delivery hours later (see worker.Dispatcher.subscriptionMatches).
	if err := celpkg.Validate(celpkg.EventEnvelopeSchema, s.CELFilter); err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("filter: %w", err))
	}
	if err := h.refuseOffKind(s, s.SinkKind, s.Disabled); err != nil {
		return nil, err
	}
	if err := h.repo.Create(ctx, &s); err != nil {
		return nil, h.failed("event subscription create failed", s, err)
	}
	// repo.Create stamps the generated SubscriptionID onto s; without
	// the pointer receiver this Get would look up the caller's zero
	// UUID and fail closed with ErrNotFound, leaving an "orphan" row
	// in the DB and surfacing a confusing "admin: resource not found"
	// CodeInternal back to the operator.
	got, err := h.readBack(ctx, s)
	if err != nil {
		return nil, err
	}
	h.log.Info("event subscription created", subFields(*got)...)
	return got, nil
}

// readBack is the row a write left, read on the scope that wrote it. A write
// that cannot be read back is reported, never answered with an empty row.
func (h *Handler) readBack(ctx context.Context, s admindomain.EventSubscription) (*admindomain.EventSubscription, error) {
	got, err := h.repo.Get(ctx, s.SubscriptionID)
	if err != nil {
		h.log.Error("event subscription written but not read back", append(subFields(s), zap.Error(err))...)
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
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
		return nil, connect.NewError(connect.CodeNotFound, "subscription not found")
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
	if writes(mask, admindomain.EventSubscriptionPathFilter) {
		if err := celpkg.Validate(celpkg.EventEnvelopeSchema, s.CELFilter); err != nil {
			return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("filter: %w", err))
		}
	}
	kind, disabled := sinkAfterUpdate(*current, s, mask)
	if err := h.refuseOffKind(*current, kind, disabled); err != nil {
		return nil, err
	}
	if err := h.repo.Update(ctx, s, expectedVersion, mask); err != nil {
		return nil, h.failed("event subscription update failed", *current, err)
	}
	got, err := h.readBack(ctx, *current)
	if err != nil {
		return nil, err
	}
	h.log.Info("event subscription updated", subFields(*got)...)
	return got, nil
}

// writes reports whether an update with mask writes path: an empty mask
// replaces the subscription whole (AIP-134).
func writes(mask []string, path string) bool {
	return len(mask) == 0 || slices.Contains(mask, path)
}

// sinkAfterUpdate is the sink kind and disabled flag the subscription has
// once update s with mask is applied to current.
func sinkAfterUpdate(current, s admindomain.EventSubscription, mask []string) (kind string, disabled bool) {
	kind, disabled = current.SinkKind, current.Disabled
	if writes(mask, admindomain.EventSubscriptionPathSink) {
		kind = s.SinkKind
	}
	if writes(mask, admindomain.EventSubscriptionPathDisabled) {
		disabled = s.Disabled
	}
	return kind, disabled
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
		return h.failed("event subscription delete failed", *current, err)
	}
	h.log.Info("event subscription deleted", subFields(*current)...)
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

// errRedriveDisabled refuses a redrive the dispatcher would undo: it fails a
// disabled subscription's deliveries as soon as it reads them.
var errRedriveDisabled = errors.New("the subscription is disabled; enable it before redriving its deliveries")

// RedriveFailedDeliveries queues the subscription's failed deliveries again
// and returns how many. It takes the same permission as editing the
// subscription.
func (h *Handler) RedriveFailedDeliveries(ctx context.Context, tenantID, id uuid.UUID) (int64, error) {
	sub, err := h.Get(ctx, tenantID, id)
	if err != nil {
		return 0, err
	}
	if ctx, err = h.authorize(ctx, cedar.ActionManageSubscription, sub.TenantID); err != nil {
		return 0, err
	}
	if sub.Disabled {
		h.log.Warn("event subscription redrive refused: disabled", subFields(*sub)...)
		return 0, connect.NewError(connect.CodeFailedPrecondition, errRedriveDisabled.Error()).WithCause(errRedriveDisabled)
	}
	n, err := h.repo.RequeueFailedDeliveries(ctx, sub.SubscriptionID)
	if err != nil {
		return 0, h.failed("event subscription redrive failed", *sub, err)
	}
	h.log.Info("event subscription failed deliveries requeued", append(subFields(*sub), zap.Int64("deliveries", n))...)
	return n, nil
}

// TestSubscription delivers a synthetic event (eventtype.SubscriptionTest)
// to the sink. Returns the delivery attempt's status as the connect-level
// error so the admin UI surfaces it directly to the operator.
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
			"event dispatcher not wired; TestSubscription unavailable")
	}
	// A test delivery is a delivery: refused for a kind that is off even
	// while the subscription itself is disabled.
	if err := h.refuseOffKind(*sub, sub.SinkKind, false); err != nil {
		return err
	}
	if err := h.dispatcher.DeliverOne(ctx, *sub, eventtype.SubscriptionTest); err != nil {
		h.log.Warn("event subscription test delivery failed", append(subFields(*sub), zap.Error(err))...)
		return rpcerr.New(connect.CodeFailedPrecondition, fmt.Errorf("test delivery failed: %w", err))
	}
	h.log.Info("event subscription test delivered", subFields(*sub)...)
	return nil
}
