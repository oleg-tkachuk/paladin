package admin

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/eventsubh"
	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1/paladinadminv1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
)

type EventSubscriptionServer struct {
	paladinadminv1connect.UnimplementedEventSubscriptionServiceHandler
	H *eventsubh.Handler
	// Tenants resolves the slug form of a subscription's resource name.
	// Subscriptions are RLS-isolated, so the handler needs the tenant's id
	// before it can read the row — see eventsubh.Handler.Get.
	Tenants TenantResolver
}

// TenantResolver is the slug → id lookup this server needs, kept as an
// interface so it does not depend on the whole tenant handler.
type TenantResolver interface {
	GetTenantBySlug(ctx context.Context, slug string) (*tenant.Tenant, error)
}

func NewEventSubscriptionServer(h *eventsubh.Handler, tenants TenantResolver) *EventSubscriptionServer {
	return &EventSubscriptionServer{H: h, Tenants: tenants}
}

// resolveSubscriptionName splits "tenants/{tenant_id_or_slug}/eventSubscriptions/{id}",
// resolving a slug to its id so the handler can scope the connection before
// reading.
func (s *EventSubscriptionServer) resolveSubscriptionName(ctx context.Context, name string) (uuid.UUID, uuid.UUID, error) {
	ref, subID, err := subscriptionFromName(name)
	if err != nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	id, err := uuid.Parse(subID)
	if err != nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if ref.HasID() {
		return ref.ID, id, nil
	}
	if s.Tenants == nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("subscription name must use the tenant uuid here"))
	}
	t, err := s.Tenants.GetTenantBySlug(ctx, ref.Slug)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return t.TenantID, id, nil
}

func (s *EventSubscriptionServer) CreateSubscription(ctx context.Context, req *connect.Request[pb.CreateSubscriptionRequest]) (*connect.Response[pb.EventSubscription], error) {
	m := req.Msg
	tenantStr, err := tenantIDFromName(m.GetParent())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	src := m.GetSubscription()
	kind, cfg := sinkToConfig(src.GetSink())
	out, err := s.H.Create(ctx, admindomain.EventSubscription{
		TenantID:   tenantID,
		CELFilter:  src.GetFilter(),
		SinkKind:   kind,
		SinkConfig: cfg,
		Disabled:   src.GetDisabled(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(eventSubToProto(out)), nil
}

func (s *EventSubscriptionServer) GetSubscription(ctx context.Context, req *connect.Request[pb.GetSubscriptionRequest]) (*connect.Response[pb.EventSubscription], error) {
	tenantID, id, err := s.resolveSubscriptionName(ctx, req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	out, err := s.H.Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(eventSubToProto(out)), nil
}

func (s *EventSubscriptionServer) UpdateSubscription(ctx context.Context, req *connect.Request[pb.UpdateSubscriptionRequest]) (*connect.Response[pb.EventSubscription], error) {
	m := req.Msg
	tenantID, id, err := s.resolveSubscriptionName(ctx, m.GetName())
	if err != nil {
		return nil, err
	}
	rv, err := parseRV(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
	src := m.GetSubscription()
	kind, cfg := sinkToConfig(src.GetSink())
	out, err := s.H.Update(ctx, tenantID, admindomain.EventSubscription{
		SubscriptionID: id,
		CELFilter:      src.GetFilter(),
		SinkKind:       kind,
		SinkConfig:     cfg,
		Disabled:       src.GetDisabled(),
	}, rv, m.GetUpdateMask().GetPaths())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(eventSubToProto(out)), nil
}

func (s *EventSubscriptionServer) DeleteSubscription(ctx context.Context, req *connect.Request[pb.DeleteSubscriptionRequest]) (*connect.Response[pb.DeleteSubscriptionResponse], error) {
	tenantID, id, err := s.resolveSubscriptionName(ctx, req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	rv, err := parseRV(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
	if err := s.H.Delete(ctx, tenantID, id, rv); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.DeleteSubscriptionResponse{}), nil
}

func (s *EventSubscriptionServer) ListSubscriptions(ctx context.Context, req *connect.Request[pb.ListSubscriptionsRequest]) (*connect.Response[pb.ListSubscriptionsResponse], error) {
	m := req.Msg
	args := admindomain.ListEventSubscriptionsArgs{PageSize: m.GetPage().GetPageSize()}
	if m.GetParent() != "" {
		if tStr, err := tenantIDFromName(m.GetParent()); err == nil {
			if id, perr := uuid.Parse(tStr); perr == nil {
				args.TenantID = id
			}
		}
	}
	if tok := m.GetPage().GetPageToken(); tok != "" {
		if id, err := uuid.Parse(tok); err == nil {
			args.AfterID = id
		}
	}
	list, next, err := s.H.List(ctx, args)
	if err != nil {
		return nil, err
	}
	out := &pb.ListSubscriptionsResponse{Page: pageResponseProto(next)}
	for i := range list {
		out.Subscriptions = append(out.Subscriptions, eventSubToProto(&list[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *EventSubscriptionServer) TestSubscription(ctx context.Context, req *connect.Request[pb.TestSubscriptionRequest]) (*connect.Response[pb.TestSubscriptionResponse], error) {
	tenantID, id, err := s.resolveSubscriptionName(ctx, req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	if err := s.H.TestSubscription(ctx, tenantID, id); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.TestSubscriptionResponse{Delivered: true}), nil
}

var _ paladinadminv1connect.EventSubscriptionServiceHandler = (*EventSubscriptionServer)(nil)
