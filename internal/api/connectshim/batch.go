package connectshim

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/v1/paladinv1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/batch"
)

type BatchServer struct {
	paladinv1connect.UnimplementedBatchServiceHandler
	H *batch.Handler
}

func NewBatchServer(h *batch.Handler) *BatchServer { return &BatchServer{H: h} }

// resolveObjectIDs expands the explicit names list. CEL filter expansion is
// deferred to the worker (handler enforces the objectKey-level auth check here).
func resolveObjectIDs(sel *pb.ObjectSelector) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(sel.GetNames()))
	for _, n := range sel.GetNames() {
		_, id, err := parseObjectName(n)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

func operationProtoFromID(id uuid.UUID, opType string) *pb.Operation {
	return &pb.Operation{
		Name: fmt.Sprintf("operations/%s", id),
		Type: opType,
	}
}

func (s *BatchServer) BatchDeleteObjects(ctx context.Context, req *connect.Request[pb.BatchDeleteObjectsRequest]) (*connect.Response[pb.Operation], error) {
	ids, err := resolveObjectIDs(req.Msg.GetSelector())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	opID, err := s.H.BatchDelete(ctx, batch.BatchDeleteArgs{
		ObjectKey: req.Msg.GetObjectKey(),
		ObjectIDs: ids,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(operationProtoFromID(opID, "BatchDelete")), nil
}

func (s *BatchServer) BatchCopyObjects(ctx context.Context, req *connect.Request[pb.BatchCopyObjectsRequest]) (*connect.Response[pb.Operation], error) {
	ids, err := resolveObjectIDs(req.Msg.GetSelector())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	opID, err := s.H.BatchCopy(ctx, batch.BatchCopyArgs{
		SrcBucket: req.Msg.GetSourceBucket(),
		DstBucket: req.Msg.GetDestinationBucket(),
		ObjectIDs: ids,
		KeyPrefix: req.Msg.GetDestinationKeyTemplate(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(operationProtoFromID(opID, "BatchCopy")), nil
}

var _ paladinv1connect.BatchServiceHandler = (*BatchServer)(nil)
