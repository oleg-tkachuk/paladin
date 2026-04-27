package connectshim

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/v1/paladinv1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/operation"
)

type OperationServer struct {
	paladinv1connect.UnimplementedOperationServiceHandler
	H *operation.Handler
}

func NewOperationServer(h *operation.Handler) *OperationServer { return &OperationServer{H: h} }

func opStateDone(s operation.State) bool {
	switch s {
	case operation.StateSucceeded, operation.StateFailed, operation.StateCancelled:
		return true
	}
	return false
}

func operationToProto(o *operation.Operation) *pb.Operation {
	if o == nil {
		return nil
	}
	out := &pb.Operation{
		Name:      fmt.Sprintf("operations/%s", o.OperationID),
		Type:      o.Type,
		Done:      opStateDone(o.State),
		CreatedAt: tsProto(o.CreatedAt),
		UpdatedAt: tsProto(o.UpdatedAt),
	}
	if len(o.Metadata) > 0 {
		// Metadata is stored as proto-Any bytes by producers; pass through verbatim.
		var a anypb.Any
		if err := proto.Unmarshal(o.Metadata, &a); err == nil {
			out.Metadata = &a
		}
	}
	if len(o.Response) > 0 {
		var a anypb.Any
		if err := proto.Unmarshal(o.Response, &a); err == nil {
			out.Result = &pb.Operation_Response{Response: &a}
		}
	}
	return out
}

func (s *OperationServer) GetOperation(ctx context.Context, req *connect.Request[pb.GetOperationRequest]) (*connect.Response[pb.Operation], error) {
	id, err := parseOperationName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	op, err := s.H.GetOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(operationToProto(op)), nil
}

func (s *OperationServer) CancelOperation(ctx context.Context, req *connect.Request[pb.CancelOperationRequest]) (*connect.Response[pb.Operation], error) {
	id, err := parseOperationName(req.Msg.GetName())
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

func (s *OperationServer) ListOperations(ctx context.Context, req *connect.Request[pb.ListOperationsRequest]) (*connect.Response[pb.ListOperationsResponse], error) {
	ops, next, err := s.H.ListOperations(ctx, nil, req.Msg.GetPageSize(), req.Msg.GetPageToken())
	if err != nil {
		return nil, err
	}
	out := &pb.ListOperationsResponse{NextPageToken: next}
	for i := range ops {
		out.Operations = append(out.Operations, operationToProto(&ops[i]))
	}
	return connect.NewResponse(out), nil
}

var _ paladinv1connect.OperationServiceHandler = (*OperationServer)(nil)
