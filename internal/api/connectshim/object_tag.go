package connectshim

import (
	"context"
	"encoding/json"
	"fmt"

	"connectrpc.com/connect"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/v1/paladinv1connect"
	objecttag "github.com/oleg-tkachuk/paladin/internal/api/v1/object_tag"
)

// ObjectTagServer bridges generated Connect to objecttag.Handler.
type ObjectTagServer struct {
	paladinv1connect.UnimplementedObjectTagServiceHandler
	H *objecttag.Handler
}

func NewObjectTagServer(h *objecttag.Handler) *ObjectTagServer { return &ObjectTagServer{H: h} }

func (s *ObjectTagServer) CreateObjectTag(ctx context.Context, req *connect.Request[pb.CreateObjectTagRequest]) (*connect.Response[pb.ObjectTag], error) {
	m := req.Msg
	labelBytes, _ := json.Marshal(m.GetLabels())
	ot, err := s.H.CreateObjectTag(ctx, objecttag.CreateArgs{
		Slug:        m.GetSlug(),
		DisplayName: m.GetDisplayName(),
		Description: m.GetDescription(),
		Labels:      labelBytes,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(ObjectTagToProto(ot)), nil
}

func (s *ObjectTagServer) GetObjectTag(ctx context.Context, req *connect.Request[pb.GetObjectTagRequest]) (*connect.Response[pb.ObjectTag], error) {
	slug, err := parseObjectTagName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	ot, err := s.H.GetObjectTag(ctx, slug)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(ObjectTagToProto(ot)), nil
}

func (s *ObjectTagServer) UpdateObjectTag(ctx context.Context, req *connect.Request[pb.UpdateObjectTagRequest]) (*connect.Response[pb.ObjectTag], error) {
	m := req.Msg
	slug, err := parseObjectTagName(m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseResourceVersion(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	args := objecttag.UpdateArgs{Slug: slug, ExpectedVersion: rv}
	for _, path := range m.GetUpdateMask().GetPaths() {
		switch path {
		case "display_name":
			v := m.GetDisplayName()
			args.DisplayName = &v
		case "description":
			v := m.GetDescription()
			args.Description = &v
		case "labels":
			b, _ := json.Marshal(m.GetLabels())
			args.Labels = b
		}
	}
	ot, err := s.H.UpdateObjectTag(ctx, args)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(ObjectTagToProto(ot)), nil
}

func (s *ObjectTagServer) DeleteObjectTag(ctx context.Context, req *connect.Request[pb.DeleteObjectTagRequest]) (*connect.Response[pb.DeleteObjectTagResponse], error) {
	slug, err := parseObjectTagName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseResourceVersion(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := s.H.DeleteObjectTag(ctx, slug, rv); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.DeleteObjectTagResponse{}), nil
}

func (s *ObjectTagServer) ListObjectTags(ctx context.Context, req *connect.Request[pb.ListObjectTagsRequest]) (*connect.Response[pb.ListObjectTagsResponse], error) {
	m := req.Msg
	cs, next, err := s.H.ListObjectTags(ctx, m.GetPageSize(), m.GetPageToken())
	if err != nil {
		return nil, err
	}
	out := &pb.ListObjectTagsResponse{NextPageToken: next}
	for i := range cs {
		out.ObjectTags = append(out.ObjectTags, ObjectTagToProto(&cs[i]))
	}
	return connect.NewResponse(out), nil
}

var _ paladinv1connect.ObjectTagServiceHandler = (*ObjectTagServer)(nil)

func parseObjectTagName(name string) (string, error) {
	const prefix = "object_tags/"
	if len(name) <= len(prefix) || name[:len(prefix)] != prefix {
		return "", fmt.Errorf("invalid object tag name %q", name)
	}
	return name[len(prefix):], nil
}

func ObjectTagToProto(c *objecttag.ObjectTag) *pb.ObjectTag {
	if c == nil {
		return nil
	}
	var labels map[string]string
	if len(c.Labels) > 0 {
		_ = json.Unmarshal(c.Labels, &labels)
	}
	return &pb.ObjectTag{
		Name:            fmt.Sprintf("object_tags/%s", c.Slug),
		Slug:            c.Slug,
		DisplayName:     c.DisplayName,
		Description:     c.Description,
		Labels:          labels,
		ResourceVersion: resourceVersion(c.ResourceVersion),
		CreatedAt:       tsProto(c.CreatedAt),
		UpdatedAt:       tsProto(c.UpdatedAt),
	}
}
