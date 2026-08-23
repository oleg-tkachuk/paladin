package data

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/batch"
)

type BatchServer struct {
	paladindatav1connect.UnimplementedBatchServiceHandler
	H *batch.Handler
}

func NewBatchServer(h *batch.Handler) *BatchServer { return &BatchServer{H: h} }

func (s *BatchServer) BatchDeleteObjects(ctx context.Context, req *connect.Request[pb.BatchDeleteObjectsRequest]) (*connect.Response[pb.Operation], error) {
	m := req.Msg
	collection, err := collectionNameParts(ctx, m.GetParent())
	if err != nil {
		return nil, badName(err)
	}
	ids, err := resolveObjectIDs(ctx, m.GetSelector())
	if err != nil {
		return nil, badName(err)
	}
	// The executor soft-deletes, always. Accepting permanent=true and doing a
	// soft delete anyway is the dangerous shape: the caller is told the data is
	// gone while it sits in the trash — an erasure request that silently did
	// not erase. Refuse until the executor can honour it.
	if m.GetPermanent() {
		return nil, connect.NewError(connect.CodeUnimplemented,
			fmt.Errorf("permanent=true is not supported by BatchDeleteObjects; "+
				"delete the objects individually with DeleteObject(permanent=true)"))
	}
	opID, err := s.H.BatchDelete(ctx, batch.BatchDeleteArgs{
		Collection: collection,
		ObjectIDs:  ids,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.Operation{
		Name: fmt.Sprintf("operations/%s", opID),
		Type: "BatchDelete",
	}), nil
}

func (s *BatchServer) BatchCopyObjects(ctx context.Context, req *connect.Request[pb.BatchCopyObjectsRequest]) (*connect.Response[pb.Operation], error) {
	m := req.Msg
	srcOK, err := collectionNameParts(ctx, m.GetSourceParent())
	if err != nil {
		return nil, badName(fmt.Errorf("source: %w", err))
	}
	dstOK, err := collectionNameParts(ctx, m.GetDestinationCollection())
	if err != nil {
		return nil, badName(fmt.Errorf("destination: %w", err))
	}
	ids, err := resolveObjectIDs(ctx, m.GetSelector())
	if err != nil {
		return nil, badName(err)
	}
	opID, err := s.H.BatchCopy(ctx, batch.BatchCopyArgs{
		SrcCollection: srcOK,
		DstCollection: dstOK,
		ObjectIDs:     ids,
		KeyPrefix:     m.GetDestinationKeyTemplate(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.Operation{
		Name: fmt.Sprintf("operations/%s", opID),
		Type: "BatchCopy",
	}), nil
}

func (s *BatchServer) BatchRestoreObjects(ctx context.Context, req *connect.Request[pb.BatchRestoreObjectsRequest]) (*connect.Response[pb.Operation], error) {
	m := req.Msg
	collection, err := collectionNameParts(ctx, m.GetParent())
	if err != nil {
		return nil, badName(err)
	}
	ids, err := resolveObjectIDs(ctx, m.GetSelector())
	if err != nil {
		return nil, badName(err)
	}
	opID, err := s.H.BatchRestoreObjects(ctx, batch.BatchRestoreObjectsArgs{
		Collection: collection,
		ObjectIDs:  ids,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.Operation{
		Name: fmt.Sprintf("operations/%s", opID),
		Type: "BatchRestoreObjects",
	}), nil
}

func (s *BatchServer) BatchUpdateTags(ctx context.Context, req *connect.Request[pb.BatchUpdateTagsRequest]) (*connect.Response[pb.Operation], error) {
	m := req.Msg
	collection, err := collectionNameParts(ctx, m.GetParent())
	if err != nil {
		return nil, badName(err)
	}
	ids, err := resolveObjectIDs(ctx, m.GetSelector())
	if err != nil {
		return nil, badName(err)
	}
	opID, err := s.H.BatchUpdateTags(ctx, batch.BatchUpdateTagsArgs{
		Collection: collection,
		ObjectIDs:  ids,
		Tags:       m.GetTags(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.Operation{
		Name: fmt.Sprintf("operations/%s", opID),
		Type: "BatchUpdateTags",
	}), nil
}

var _ paladindatav1connect.BatchServiceHandler = (*BatchServer)(nil)

// resolveObjectIDs decodes the explicit `names` part of an ObjectSelector
// into UUIDs. The CEL `filter` form is expanded inside the worker.
func resolveObjectIDs(ctx context.Context, sel *pb.ObjectSelector) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(sel.GetNames()))
	for _, n := range sel.GetNames() {
		_, idStr, err := objectNameParts(ctx, n)
		if err != nil {
			return nil, err
		}
		id, err := uuid.Parse(idStr)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}
