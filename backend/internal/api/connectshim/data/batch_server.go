package data

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/batchh"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
)

type BatchServer struct {
	paladindatav1connect.UnimplementedBatchServiceHandler
	H batchHandler
}

func NewBatchServer(h *batchh.Handler) *BatchServer { return &BatchServer{H: h} }

func (s *BatchServer) BatchDeleteObjects(ctx context.Context, req *pb.BatchDeleteObjectsRequest) (*pb.Operation, error) {
	m := req
	ctx, collection, err := collectionNameParts(ctx, m.GetParent())
	if err != nil {
		return nil, badName(err)
	}
	ids, err := resolveObjectIDs(ctx, m.GetSelector())
	if err != nil {
		return nil, badName(err)
	}
	opID, err := s.H.BatchDelete(ctx, batchh.BatchDeleteArgs{
		Collection: collection,
		ObjectIDs:  ids,
		Permanent:  m.GetPermanent(),
	})
	if err != nil {
		return nil, err
	}
	return &pb.Operation{
		Name: fmt.Sprintf("operations/%s", opID),
		Type: "BatchDelete",
	}, nil
}

func (s *BatchServer) BatchCopyObjects(ctx context.Context, req *pb.BatchCopyObjectsRequest) (*pb.Operation, error) {
	m := req
	ctx, srcOK, err := collectionNameParts(ctx, m.GetSourceParent())
	if err != nil {
		return nil, badName(fmt.Errorf("source: %w", err))
	}
	ctx, dstOK, err := collectionNameParts(ctx, m.GetDestinationCollection())
	if err != nil {
		return nil, badName(fmt.Errorf("destination: %w", err))
	}
	ids, err := resolveObjectIDs(ctx, m.GetSelector())
	if err != nil {
		return nil, badName(err)
	}
	opID, err := s.H.BatchCopy(ctx, batchh.BatchCopyArgs{
		SrcCollection: srcOK,
		DstCollection: dstOK,
		ObjectIDs:     ids,
		KeyPrefix:     m.GetDestinationKeyTemplate(),
	})
	if err != nil {
		return nil, err
	}
	return &pb.Operation{
		Name: fmt.Sprintf("operations/%s", opID),
		Type: "BatchCopy",
	}, nil
}

func (s *BatchServer) BatchRestoreObjects(ctx context.Context, req *pb.BatchRestoreObjectsRequest) (*pb.Operation, error) {
	m := req
	ctx, collection, err := collectionNameParts(ctx, m.GetParent())
	if err != nil {
		return nil, badName(err)
	}
	ids, err := resolveObjectIDs(ctx, m.GetSelector())
	if err != nil {
		return nil, badName(err)
	}
	opID, err := s.H.BatchRestoreObjects(ctx, batchh.BatchRestoreObjectsArgs{
		Collection: collection,
		ObjectIDs:  ids,
	})
	if err != nil {
		return nil, err
	}
	return &pb.Operation{
		Name: fmt.Sprintf("operations/%s", opID),
		Type: "BatchRestoreObjects",
	}, nil
}

func (s *BatchServer) BatchUpdateTags(ctx context.Context, req *pb.BatchUpdateTagsRequest) (*pb.Operation, error) {
	m := req
	ctx, collection, err := collectionNameParts(ctx, m.GetParent())
	if err != nil {
		return nil, badName(err)
	}
	ids, err := resolveObjectIDs(ctx, m.GetSelector())
	if err != nil {
		return nil, badName(err)
	}
	opID, err := s.H.BatchUpdateTags(ctx, batchh.BatchUpdateTagsArgs{
		Collection: collection,
		ObjectIDs:  ids,
		Tags:       m.GetTags(),
		Replace:    m.GetReplace(),
	})
	if err != nil {
		return nil, err
	}
	return &pb.Operation{
		Name: fmt.Sprintf("operations/%s", opID),
		Type: "BatchUpdateTags",
	}, nil
}

var _ paladindatav1connect.BatchServiceHandler = (*BatchServer)(nil)

// resolveObjectIDs decodes the explicit `names` part of an ObjectSelector
// into UUIDs. The CEL `filter` form is expanded inside the worker.
func resolveObjectIDs(ctx context.Context, sel *pb.ObjectSelector) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(sel.GetNames()))
	for _, n := range sel.GetNames() {
		// Checked against the parent's tenant, which ctx already names.
		_, _, idStr, err := objectNameParts(ctx, n)
		if err != nil {
			return nil, err
		}
		// objectNameParts has parsed the id and returns it canonical, so it
		// cannot fail here; a check would be a branch no request can reach.
		out = append(out, uuid.MustParse(idStr))
	}
	return out, nil
}
