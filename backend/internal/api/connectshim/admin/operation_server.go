package admin

import (
	"context"
	"fmt"
	"strings"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"
	commonpb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
)

type OperationServer struct {
	paladinadminv1connect.UnimplementedPlatformOperationServiceHandler
	H operationHandler
}

func NewOperationServer(h *operationh.Handler) *OperationServer { return &OperationServer{H: h} }

func (s *OperationServer) GetOperation(ctx context.Context, req *pb.GetOperationRequest) (*pb.Operation, error) {
	id, err := operationID(req.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	op, err := s.H.GetOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (s *OperationServer) ListOperations(ctx context.Context, req *pb.ListOperationsRequest) (*pb.ListOperationsResponse, error) {
	m := req
	list, next, err := s.H.ListOperations(ctx, nil,
		m.GetPage().GetPageSize(), m.GetPage().GetPageToken(), m.GetFilter(),
		m.GetSortOrder() == commonpb.SortOrder_SORT_ORDER_DESC)
	if err != nil {
		return nil, err
	}
	out := &pb.ListOperationsResponse{Page: convx.PageResponseProto(next)}
	for i := range list {
		out.Operations = append(out.Operations, operationToProto(&list[i]))
	}
	return out, nil
}

func (s *OperationServer) CancelOperation(ctx context.Context, req *pb.CancelOperationRequest) (*pb.Operation, error) {
	id, err := operationID(req.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	if err := s.H.CancelOperation(ctx, id); err != nil {
		return nil, err
	}
	op, err := s.H.GetOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

var _ paladinadminv1connect.PlatformOperationServiceHandler = (*OperationServer)(nil)

func operationID(name string) (uuid.UUID, error) {
	const prefix = "operations/"
	if !strings.HasPrefix(name, prefix) || len(name) <= len(prefix) {
		return uuid.Nil, fmt.Errorf("invalid operation name %q", name)
	}
	return uuid.Parse(name[len(prefix):])
}

func operationToProto(o *operationh.Operation) *pb.Operation {
	if o == nil {
		return nil
	}
	out := &pb.Operation{
		Name:              fmt.Sprintf("operations/%s", o.OperationID),
		Type:              o.Type,
		Done:              o.State == operationh.StateSucceeded || o.State == operationh.StateFailed || o.State == operationh.StateCancelled,
		InitiatorTenantId: o.TenantID.String(),
		CreatedAt:         convx.TsProto(o.CreatedAt),
		UpdatedAt:         convx.TsProto(o.UpdatedAt),
	}
	if md := convx.JSONToAny(o.Metadata); md != nil {
		out.Metadata = md
	}
	switch o.State {
	case operationh.StateFailed, operationh.StateCancelled:
		out.Result = &pb.Operation_Error{
			Error: convx.OperationError(o.ErrorCode, o.ErrorMessage, o.Response,
				o.State == operationh.StateCancelled),
		}
	default:
		if resp := convx.JSONToAny(o.Response); resp != nil {
			out.Result = &pb.Operation_Response{Response: resp}
		}
	}
	return out
}
