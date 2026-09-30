package data

import (
	"context"
	"fmt"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"

	"connectrpc.com/connect"

	commonpb "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/common/v1"
	pb "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/data/v1"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/pb/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/object"
)

// ObjectTagServer implements per-object tag CRUD on top of object.Handler.
// Distinct from the v1 object_tag taxonomy service (tenant-scoped slug
// dictionary) — that lives in the admin plane only.
type ObjectTagServer struct {
	paladindatav1connect.UnimplementedObjectTagServiceHandler
	H objectTagHandler
}

func NewObjectTagServer(h *object.Handler) *ObjectTagServer { return &ObjectTagServer{H: h} }

func (s *ObjectTagServer) GetObjectTags(ctx context.Context, req *connect.Request[pb.GetObjectTagsRequest]) (*connect.Response[pb.GetObjectTagsResponse], error) {
	collection, objectID, err := objectNameParts(ctx, req.Msg.GetName())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.H.GetObject(ctx, collection, objectID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.GetObjectTagsResponse{Tags: out.Tags}), nil
}

func (s *ObjectTagServer) PutObjectTags(ctx context.Context, req *connect.Request[pb.PutObjectTagsRequest]) (*connect.Response[pb.PutObjectTagsResponse], error) {
	m := req.Msg
	collection, objectID, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, badName(err)
	}
	rv, err := convx.ParseRV(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
	out, err := s.H.UpdateObject(ctx, object.UpdateObjectInput{
		Collection:      collection,
		ObjectID:        objectID,
		ResourceVersion: rv,
		UpdatedFields:   []string{"tags"},
		Tags:            m.GetTags(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.PutObjectTagsResponse{Tags: out.Tags}), nil
}

func (s *ObjectTagServer) DeleteObjectTags(ctx context.Context, req *connect.Request[pb.DeleteObjectTagsRequest]) (*connect.Response[pb.DeleteObjectTagsResponse], error) {
	m := req.Msg
	collection, objectID, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, badName(err)
	}
	rv, err := convx.ParseRV(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
	current, err := s.H.GetObject(ctx, collection, objectID)
	if err != nil {
		return nil, err
	}
	updated := map[string]string{}
	if len(m.GetKeys()) == 0 {
		// Empty keys → drop all tags.
	} else {
		// Keep keys not in the delete set.
		drop := map[string]struct{}{}
		for _, k := range m.GetKeys() {
			drop[k] = struct{}{}
		}
		for k, v := range current.Tags {
			if _, found := drop[k]; !found {
				updated[k] = v
			}
		}
	}
	out, err := s.H.UpdateObject(ctx, object.UpdateObjectInput{
		Collection:      collection,
		ObjectID:        objectID,
		ResourceVersion: rv,
		UpdatedFields:   []string{"tags"},
		Tags:            updated,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.DeleteObjectTagsResponse{Tags: out.Tags}), nil
}

func (s *ObjectTagServer) ListDistinctTags(ctx context.Context, req *connect.Request[pb.ListDistinctTagsRequest]) (*connect.Response[pb.ListDistinctTagsResponse], error) {
	collection, err := collectionNameParts(ctx, req.Msg.GetParent())
	if err != nil {
		return nil, badName(err)
	}
	page, err := s.H.ListDistinctTags(ctx, collection,
		req.Msg.GetPage().GetPageToken(), req.Msg.GetPage().GetPageSize())
	if err != nil {
		return nil, err
	}
	out := &pb.ListDistinctTagsResponse{
		Tags: make(map[string]*pb.TagValues, len(page.Keys)),
	}
	for _, k := range page.Keys {
		out.Tags[k] = &pb.TagValues{
			Values:    page.Values[k],
			Truncated: page.Truncated[k],
		}
	}
	if page.NextKey != "" {
		out.Page = &commonpb.PageResponse{NextPageToken: page.NextKey}
	}
	return connect.NewResponse(out), nil
}

var _ paladindatav1connect.ObjectTagServiceHandler = (*ObjectTagServer)(nil)
