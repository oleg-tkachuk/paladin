package data

import (
	"context"
	"fmt"
	"strings"
	"uuid"

	"connectrpc.com/connect/v2"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"
	"github.com/oleg-tkachuk/paladin/backend/internal/safecast"
	commonpb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
)

// OperationServer is the data-plane mirror — caller sees only operations
// they initiated (filtering is enforced inside the v1 operation.Handler via
// JWT tenant context).
type OperationServer struct {
	paladindatav1connect.UnimplementedOperationServiceHandler
	H operationHandler
}

func NewOperationServer(h *operationh.Handler) *OperationServer { return &OperationServer{H: h} }

func (s *OperationServer) GetOperation(ctx context.Context, req *pb.GetOperationRequest) (*pb.Operation, error) {
	id, err := dataOperationID(req.GetName())
	if err != nil {
		return nil, badName(err)
	}
	op, err := s.H.GetOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	return dataOperationToProto(op), nil
}

func (s *OperationServer) ListOperations(ctx context.Context, req *pb.ListOperationsRequest) (*pb.ListOperationsResponse, error) {
	m := req
	in := operationh.ListInput{
		PageSize:         m.GetPage().GetPageSize(),
		PageToken:        m.GetPage().GetPageToken(),
		Filter:           m.GetFilter(),
		NewestFirst:      m.GetSortOrder() == commonpb.SortOrder_SORT_ORDER_DESC,
		Mine:             m.GetMine(),
		IncludeDismissed: m.GetIncludeDismissed(),
		Facets:           convx.OperationFacetsFromProto(m.GetFacets()),
	}
	list, next, err := s.H.ListOperations(ctx, in)
	if err != nil {
		return nil, err
	}
	out := &pb.ListOperationsResponse{Page: convx.PageResponseProto(next)}
	for i := range list {
		out.Operations = append(out.Operations, dataOperationToProto(&list[i]))
	}
	// Counted with the first page only: a later page is the same listing.
	if m.GetCountFacets() && in.PageToken == "" {
		counts, err := s.H.CountOperationFacets(ctx, in)
		if err != nil {
			return nil, err
		}
		out.FacetCounts = convx.FacetCountsToProto(counts)
	}
	return out, nil
}

func (s *OperationServer) DismissOperations(ctx context.Context, req *pb.DismissOperationsRequest) (*pb.DismissOperationsResponse, error) {
	ids := make([]uuid.UUID, 0, len(req.GetNames()))
	for _, name := range req.GetNames() {
		id, err := dataOperationID(name)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
		}
		ids = append(ids, id)
	}
	n, err := s.H.DismissOperations(ctx, ids)
	if err != nil {
		return nil, err
	}
	return &pb.DismissOperationsResponse{DismissedCount: safecast.Int32From64(n)}, nil
}

func (s *OperationServer) CancelOperation(ctx context.Context, req *pb.CancelOperationRequest) (*pb.Operation, error) {
	id, err := dataOperationID(req.GetName())
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
	return dataOperationToProto(op), nil
}

var _ paladindatav1connect.OperationServiceHandler = (*OperationServer)(nil)

func dataOperationID(name string) (uuid.UUID, error) {
	const prefix = "operations/"
	if !strings.HasPrefix(name, prefix) || len(name) <= len(prefix) {
		return uuid.Nil(), fmt.Errorf("invalid operation name %q", name)
	}
	return uuid.Parse(name[len(prefix):])
}

func dataOperationToProto(o *operationh.Operation) *pb.Operation {
	if o == nil {
		return nil
	}
	out := &pb.Operation{
		Name:             fmt.Sprintf("operations/%s", o.OperationID),
		Type:             o.Type,
		Done:             o.State == operationh.StateSucceeded || o.State == operationh.StateFailed || o.State == operationh.StateCancelled,
		CreatedAt:        convx.TsProto(o.CreatedAt),
		UpdatedAt:        convx.TsProto(o.UpdatedAt),
		State:            convx.OperationState(o.State),
		InitiatorSubject: o.InitiatorSubject,
		Target:           convx.OperationTarget(o.Type, o.Metadata),
		FailedCount:      o.FailedCount,
	}
	// The API's metadata is the operation's progress (AIP-151). The executor's
	// arguments stay internal: they are the worker's input, not a report.
	if o.DoneAt != nil {
		out.DoneAt = convx.TsProto(*o.DoneAt)
	}
	if o.DismissedAt != nil {
		out.DismissedAt = convx.TsProto(*o.DismissedAt)
	}
	if o.ExpireAt != nil {
		out.ExpireTime = convx.TsProto(*o.ExpireAt)
	}
	if md := convx.JSONToAny(o.Progress); md != nil {
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
