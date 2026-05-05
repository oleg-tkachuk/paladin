// Package eventsubh implements the admin EventSubscriptionService.
package eventsubh

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
)

type Handler struct {
	repo       admindomain.EventSubscriptionRepository
	dispatcher Dispatcher
}

func NewHandler(r admindomain.EventSubscriptionRepository) *Handler { return &Handler{repo: r} }

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
	if err := h.repo.Create(ctx, s); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	got, err := h.repo.Get(ctx, s.SubscriptionID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &got, nil
}

func (h *Handler) Get(ctx context.Context, id uuid.UUID) (*admindomain.EventSubscription, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	s, err := h.repo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, admindomain.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) && s.TenantID != caller {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("subscription not found"))
	}
	return &s, nil
}

func (h *Handler) Update(ctx context.Context, s admindomain.EventSubscription, expectedVersion int64, mask []string) (*admindomain.EventSubscription, error) {
	if _, err := h.Get(ctx, s.SubscriptionID); err != nil {
		return nil, err
	}
	if err := h.repo.Update(ctx, s, expectedVersion, mask); err != nil {
		if errors.Is(err, admindomain.ErrVersionMismatch) {
			return nil, connect.NewError(connect.CodeAborted, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	got, _ := h.repo.Get(ctx, s.SubscriptionID)
	return &got, nil
}

func (h *Handler) Delete(ctx context.Context, id uuid.UUID, expectedVersion int64) error {
	if _, err := h.Get(ctx, id); err != nil {
		return err
	}
	if err := h.repo.Delete(ctx, id, expectedVersion); err != nil {
		if errors.Is(err, admindomain.ErrVersionMismatch) {
			return connect.NewError(connect.CodeAborted, err)
		}
		return connect.NewError(connect.CodeInternal, err)
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
func (h *Handler) TestSubscription(ctx context.Context, id uuid.UUID) error {
	sub, err := h.Get(ctx, id)
	if err != nil {
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
