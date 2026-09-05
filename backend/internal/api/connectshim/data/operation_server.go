package data

import (
	"context"
	"fmt"
	"strings"

	"github.com/oleg-tkachuk/paladin/internal/api/connectshim/convx"
	commonpb "github.com/oleg-tkachuk/paladin/internal/api/pb/common/v1"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/operation"
)

// OperationServer is the data-plane mirror — caller sees only operations
// they initiated (filtering is enforced inside the v1 operation.Handler via
// JWT tenant context).
type OperationServer struct {
	paladindatav1connect.UnimplementedOperationServiceHandler
	H operationHandler
}

func NewOperationServer(h *operation.Handler) *OperationServer { return &OperationServer{H: h} }

func (s *OperationServer) GetOperation(ctx context.Context, req *connect.Request[pb.GetOperationRequest]) (*connect.Response[pb.Operation], error) {
	id, err := dataOperationID(req.Msg.GetName())
	if err != nil {
		return nil, badName(err)
	}
	op, err := s.H.GetOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(dataOperationToProto(op)), nil
}

func (s *OperationServer) ListOperations(ctx context.Context, req *connect.Request[pb.ListOperationsRequest]) (*connect.Response[pb.ListOperationsResponse], error) {
	m := req.Msg
	list, next, err := s.H.ListOperations(ctx, nil,
		m.GetPage().GetPageSize(), m.GetPage().GetPageToken(), m.GetFilter(),
		m.GetSortOrder() == commonpb.SortOrder_SORT_ORDER_DESC)
	if err != nil {
		return nil, err
	}
	out := &pb.ListOperationsResponse{Page: convx.PageResponseProto(next)}
	for i := range list {
		out.Operations = append(out.Operations, dataOperationToProto(&list[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *OperationServer) CancelOperation(ctx context.Context, req *connect.Request[pb.CancelOperationRequest]) (*connect.Response[pb.Operation], error) {
	id, err := dataOperationID(req.Msg.GetName())
	if err != nil {
		return nil, badName(err)
	}
	if err := s.H.CancelOperation(ctx, id); err != nil {
		return nil, err
	}
	op, err := s.H.GetOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(dataOperationToProto(op)), nil
}

var _ paladindatav1connect.OperationServiceHandler = (*OperationServer)(nil)

func dataOperationID(name string) (uuid.UUID, error) {
	const prefix = "operations/"
	if !strings.HasPrefix(name, prefix) || len(name) <= len(prefix) {
		return uuid.Nil, fmt.Errorf("invalid operation name %q", name)
	}
	return uuid.Parse(name[len(prefix):])
}

func dataOperationToProto(o *operation.Operation) *pb.Operation {
	if o == nil {
		return nil
	}
	out := &pb.Operation{
		Name:      fmt.Sprintf("operations/%s", o.OperationID),
		Type:      o.Type,
		Done:      o.State == operation.StateSucceeded || o.State == operation.StateFailed || o.State == operation.StateCancelled,
		CreatedAt: convx.TsProto(o.CreatedAt),
		UpdatedAt: convx.TsProto(o.UpdatedAt),
	}
	if md := convx.JSONToAny(o.Metadata); md != nil {
		out.Metadata = md
	}
	switch o.State {
	case operation.StateFailed, operation.StateCancelled:
		out.Result = &pb.Operation_Error{
			Error: convx.OperationError(o.ErrorCode, o.ErrorMessage, o.Response,
				o.State == operation.StateCancelled),
		}
	default:
		if resp := convx.JSONToAny(o.Response); resp != nil {
			out.Result = &pb.Operation_Response{Response: resp}
		}
	}
	return out
}
