// Package category implements CategoryService business logic. Categories
// are tenant-scoped taxonomy entries; objects are not FK-linked to them
// (see migration 002).
package category

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/auth"
)

type Category struct {
	TenantID        uuid.UUID
	Slug            string
	DisplayName     string
	Description     string
	Labels          []byte // JSONB
	ResourceVersion int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type CreateArgs struct {
	TenantID    uuid.UUID
	Slug        string
	DisplayName string
	Description string
	Labels      []byte
}

type UpdateArgs struct {
	TenantID        uuid.UUID
	Slug            string
	ExpectedVersion int64
	DisplayName     *string
	Description     *string
	Labels          []byte
}

type Repository interface {
	Create(ctx context.Context, args CreateArgs) (Category, error)
	Get(ctx context.Context, tenantID uuid.UUID, slug string) (Category, error)
	Update(ctx context.Context, args UpdateArgs) (Category, error)
	Delete(ctx context.Context, tenantID uuid.UUID, slug string, expectedVersion int64) error
	List(ctx context.Context, tenantID uuid.UUID, pageSize int32, afterSlug string) ([]Category, string, error)
}

type Handler struct {
	repo Repository
}

func NewHandler(repo Repository) *Handler { return &Handler{repo: repo} }

func (h *Handler) CreateCategory(ctx context.Context, args CreateArgs) (*Category, error) {
	t, err := auth.TenantFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	args.TenantID = t
	c, err := h.repo.Create(ctx, args)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create category: %w", err))
	}
	return &c, nil
}

func (h *Handler) GetCategory(ctx context.Context, slug string) (*Category, error) {
	t, err := auth.TenantFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	c, err := h.repo.Get(ctx, t, slug)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return &c, nil
}

func (h *Handler) UpdateCategory(ctx context.Context, args UpdateArgs) (*Category, error) {
	t, err := auth.TenantFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	args.TenantID = t
	c, err := h.repo.Update(ctx, args)
	if err != nil {
		if errors.Is(err, ErrVersionMismatch) {
			return nil, connect.NewError(connect.CodeAborted, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &c, nil
}

func (h *Handler) DeleteCategory(ctx context.Context, slug string, expectedVersion int64) error {
	t, err := auth.TenantFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	if err := h.repo.Delete(ctx, t, slug, expectedVersion); err != nil {
		if errors.Is(err, ErrVersionMismatch) {
			return connect.NewError(connect.CodeAborted, err)
		}
		return connect.NewError(connect.CodeInternal, err)
	}
	return nil
}

func (h *Handler) ListCategories(ctx context.Context, pageSize int32, pageToken string) ([]Category, string, error) {
	t, err := auth.TenantFromContext(ctx)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeUnauthenticated, err)
	}
	return h.repo.List(ctx, t, pageSize, pageToken)
}

var ErrVersionMismatch = errors.New("resource_version mismatch")
