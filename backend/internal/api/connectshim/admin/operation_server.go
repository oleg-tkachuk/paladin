package admin

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/anypb"

	pb "github.com/oleg-tkachuk/paladin-private/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin-private/internal/api/pb/admin/v1/paladinadminv1connect"
	"github.com/oleg-tkachuk/paladin-private/internal/api/v1/operation"
)

type OperationServer struct {
	paladinadminv1connect.UnimplementedOperationServiceHandler
	H *operation.Handler
}

func NewOperationServer(h *operation.Handler) *OperationServer { return &OperationServer{H: h} }

func (s *OperationServer) GetOperation(ctx context.Context, req *connect.Request[pb.GetOperationRequest]) (*connect.Response[pb.Operation], error) {
	id, err := operationID(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	op, err := s.H.GetOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(operationToProto(op)), nil
}

func (s *OperationServer) ListOperations(ctx context.Context, req *connect.Request[pb.ListOperationsRequest]) (*connect.Response[pb.ListOperationsResponse], error) {
	m := req.Msg
	list, next, err := s.H.ListOperations(ctx, nil, m.GetPage().GetPageSize(), m.GetPage().GetPageToken())
	if err != nil {
		return nil, err
	}
	out := &pb.ListOperationsResponse{Page: pageResponseProto(next)}
	for i := range list {
		out.Operations = append(out.Operations, operationToProto(&list[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *OperationServer) CancelOperation(ctx context.Context, req *connect.Request[pb.CancelOperationRequest]) (*connect.Response[pb.Operation], error) {
	id, err := operationID(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := s.H.CancelOperation(ctx, id); err != nil {
		return nil, err
	}
	op, err := s.H.GetOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(operationToProto(op)), nil
}

var _ paladinadminv1connect.OperationServiceHandler = (*OperationServer)(nil)

func operationID(name string) (uuid.UUID, error) {
	const prefix = "operations/"
	if !strings.HasPrefix(name, prefix) || len(name) <= len(prefix) {
		return uuid.Nil, fmt.Errorf("invalid operation name %q", name)
	}
	return uuid.Parse(name[len(prefix):])
}

func operationToProto(o *operation.Operation) *pb.Operation {
	if o == nil {
		return nil
	}
	out := &pb.Operation{
		Name:              fmt.Sprintf("operations/%s", o.OperationID),
		Type:              o.Type,
		Done:              o.State == operation.StateSucceeded || o.State == operation.StateFailed || o.State == operation.StateCancelled,
		InitiatorTenantId: o.TenantID.String(),
		CreatedAt:         tsProto(o.CreatedAt),
		UpdatedAt:         tsProto(o.UpdatedAt),
	}
	if len(o.Metadata) > 0 {
		out.Metadata = &anypb.Any{Value: o.Metadata}
	}
	if len(o.Response) > 0 {
		out.Result = &pb.Operation_Response{Response: &anypb.Any{Value: o.Response}}
	}
	return out
}
