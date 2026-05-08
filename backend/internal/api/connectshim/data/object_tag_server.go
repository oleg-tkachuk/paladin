package data

import (
	"context"

	"connectrpc.com/connect"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
)

// ObjectTagServer implements per-object tag CRUD on top of object.Handler.
// Distinct from the v1 object_tag taxonomy service (tenant-scoped slug
// dictionary) — that lives in the admin plane only.
type ObjectTagServer struct {
	paladindatav1connect.UnimplementedObjectTagServiceHandler
	H *object.Handler
}

func NewObjectTagServer(h *object.Handler) *ObjectTagServer { return &ObjectTagServer{H: h} }

func (s *ObjectTagServer) GetObjectTags(ctx context.Context, req *connect.Request[pb.GetObjectTagsRequest]) (*connect.Response[pb.GetObjectTagsResponse], error) {
	objectKey, objectID, err := objectNameParts(ctx, req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.GetObject(ctx, objectKey, objectID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.GetObjectTagsResponse{Tags: out.Tags}), nil
}

func (s *ObjectTagServer) PutObjectTags(ctx context.Context, req *connect.Request[pb.PutObjectTagsRequest]) (*connect.Response[pb.PutObjectTagsResponse], error) {
	m := req.Msg
	objectKey, objectID, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseRV(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.UpdateObject(ctx, object.UpdateObjectInput{
		ObjectKey:       objectKey,
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
	objectKey, objectID, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseRV(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	current, err := s.H.GetObject(ctx, objectKey, objectID)
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
		ObjectKey:       objectKey,
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

var _ paladindatav1connect.ObjectTagServiceHandler = (*ObjectTagServer)(nil)
