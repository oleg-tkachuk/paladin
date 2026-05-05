package admin

import (
	"context"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/eventsubh"
	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1/paladinadminv1connect"
)

type EventSubscriptionServer struct {
	paladinadminv1connect.UnimplementedEventSubscriptionServiceHandler
	H *eventsubh.Handler
}

func NewEventSubscriptionServer(h *eventsubh.Handler) *EventSubscriptionServer {
	return &EventSubscriptionServer{H: h}
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
	idStr, err := subscriptionIDFromName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(eventSubToProto(out)), nil
}

func (s *EventSubscriptionServer) UpdateSubscription(ctx context.Context, req *connect.Request[pb.UpdateSubscriptionRequest]) (*connect.Response[pb.EventSubscription], error) {
	m := req.Msg
	idStr, err := subscriptionIDFromName(m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, _ := parseRV(m.GetResourceVersion())
	src := m.GetSubscription()
	kind, cfg := sinkToConfig(src.GetSink())
	out, err := s.H.Update(ctx, admindomain.EventSubscription{
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
	idStr, err := subscriptionIDFromName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, _ := parseRV(req.Msg.GetResourceVersion())
	if err := s.H.Delete(ctx, id, rv); err != nil {
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
	idStr, err := subscriptionIDFromName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := s.H.TestSubscription(ctx, id); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.TestSubscriptionResponse{Delivered: true}), nil
}

var _ paladinadminv1connect.EventSubscriptionServiceHandler = (*EventSubscriptionServer)(nil)
