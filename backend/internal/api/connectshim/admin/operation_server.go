package admin

import (
	"context"
	"fmt"
	"strings"

	"github.com/oleg-tkachuk/paladin/internal/api/connectshim/convx"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1/paladinadminv1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/operation"
)

type OperationServer struct {
	paladinadminv1connect.UnimplementedPlatformOperationServiceHandler
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
	list, next, err := s.H.ListOperations(ctx, nil, m.GetPage().GetPageSize(), m.GetPage().GetPageToken(), m.GetFilter())
	if err != nil {
		return nil, err
	}
	out := &pb.ListOperationsResponse{Page: convx.PageResponseProto(next)}
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

var _ paladinadminv1connect.PlatformOperationServiceHandler = (*OperationServer)(nil)

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
		CreatedAt:         convx.TsProto(o.CreatedAt),
		UpdatedAt:         convx.TsProto(o.UpdatedAt),
	}
	if md := jsonToAny(o.Metadata); md != nil {
		out.Metadata = md
	}
	if resp := jsonToAny(o.Response); resp != nil {
		out.Result = &pb.Operation_Response{Response: resp}
	}
	return out
}

// jsonToAny wraps an executor's JSON payload in an Any the wire can carry.
//
// operations.metadata / .response hold JSON produced by json.Marshal in the
// worker, not a serialized proto. The previous code put those bytes straight
// into &anypb.Any{Value: ...} with no TypeUrl, which proto refuses to marshal:
// "google.protobuf.Any: type_url is not set". Every ListOperations and
// GetOperation carrying a payload failed with CodeInternal — invisible for as
// long as the listing came back empty for an unrelated reason.
//
// Decoding into a Struct keeps the payload readable to any client instead of
// making it an opaque blob, and gives the Any a real type_url. A payload that
// is not a JSON object (nothing writes one today) is dropped rather than
// failing the whole response.
func jsonToAny(raw []byte) *anypb.Any {
	if len(raw) == 0 {
		return nil
	}
	var st structpb.Struct
	if err := protojson.Unmarshal(raw, &st); err != nil {
		return nil
	}
	packed, err := anypb.New(&st)
	if err != nil {
		return nil
	}
	return packed
}
