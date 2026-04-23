package connectshim

import (
	"context"
	"encoding/json"
	"fmt"

	"connectrpc.com/connect"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/v1/paladinv1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/category"
)

// CategoryServer bridges generated Connect to category.Handler.
type CategoryServer struct {
	paladinv1connect.UnimplementedCategoryServiceHandler
	H *category.Handler
}

func NewCategoryServer(h *category.Handler) *CategoryServer { return &CategoryServer{H: h} }

func (s *CategoryServer) CreateCategory(ctx context.Context, req *connect.Request[pb.CreateCategoryRequest]) (*connect.Response[pb.Category], error) {
	m := req.Msg
	labelBytes, _ := json.Marshal(m.GetLabels())
	c, err := s.H.CreateCategory(ctx, category.CreateArgs{
		Slug:        m.GetSlug(),
		DisplayName: m.GetDisplayName(),
		Description: m.GetDescription(),
		Labels:      labelBytes,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(categoryToProto(c)), nil
}

func (s *CategoryServer) GetCategory(ctx context.Context, req *connect.Request[pb.GetCategoryRequest]) (*connect.Response[pb.Category], error) {
	slug, err := parseCategoryName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	c, err := s.H.GetCategory(ctx, slug)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(categoryToProto(c)), nil
}

func (s *CategoryServer) UpdateCategory(ctx context.Context, req *connect.Request[pb.UpdateCategoryRequest]) (*connect.Response[pb.Category], error) {
	m := req.Msg
	slug, err := parseCategoryName(m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseResourceVersion(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	args := category.UpdateArgs{Slug: slug, ExpectedVersion: rv}
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
	c, err := s.H.UpdateCategory(ctx, args)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(categoryToProto(c)), nil
}

func (s *CategoryServer) DeleteCategory(ctx context.Context, req *connect.Request[pb.DeleteCategoryRequest]) (*connect.Response[pb.DeleteCategoryResponse], error) {
	slug, err := parseCategoryName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseResourceVersion(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := s.H.DeleteCategory(ctx, slug, rv); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.DeleteCategoryResponse{}), nil
}

func (s *CategoryServer) ListCategories(ctx context.Context, req *connect.Request[pb.ListCategoriesRequest]) (*connect.Response[pb.ListCategoriesResponse], error) {
	m := req.Msg
	cs, next, err := s.H.ListCategories(ctx, m.GetPageSize(), m.GetPageToken())
	if err != nil {
		return nil, err
	}
	out := &pb.ListCategoriesResponse{NextPageToken: next}
	for i := range cs {
		out.Categories = append(out.Categories, categoryToProto(&cs[i]))
	}
	return connect.NewResponse(out), nil
}

var _ paladinv1connect.CategoryServiceHandler = (*CategoryServer)(nil)

func parseCategoryName(name string) (string, error) {
	const prefix = "categories/"
	if len(name) <= len(prefix) || name[:len(prefix)] != prefix {
		return "", fmt.Errorf("invalid category name %q", name)
	}
	return name[len(prefix):], nil
}

func categoryToProto(c *category.Category) *pb.Category {
	if c == nil {
		return nil
	}
	var labels map[string]string
	if len(c.Labels) > 0 {
		_ = json.Unmarshal(c.Labels, &labels)
	}
	return &pb.Category{
		Name:            fmt.Sprintf("categories/%s", c.Slug),
		Slug:            c.Slug,
		DisplayName:     c.DisplayName,
		Description:     c.Description,
		Labels:          labels,
		ResourceVersion: resourceVersion(c.ResourceVersion),
		CreatedAt:       tsProto(c.CreatedAt),
		UpdatedAt:       tsProto(c.UpdatedAt),
	}
}
