package adapters

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/category"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// CategoryRepo satisfies category.Repository.
type CategoryRepo struct {
	q *sqlc.Queries
}

func NewCategoryRepo(q *sqlc.Queries) *CategoryRepo { return &CategoryRepo{q: q} }

var _ category.Repository = (*CategoryRepo)(nil)

func (r *CategoryRepo) Create(ctx context.Context, args category.CreateArgs) (category.Category, error) {
	if err := r.q.CreateCategory(ctx,
		pgUUID(args.TenantID),
		args.Slug,
		strPtr(args.DisplayName),
		args.Description,
		args.Labels,
	); err != nil {
		return category.Category{}, fmt.Errorf("create category: %w", err)
	}
	return r.Get(ctx, args.TenantID, args.Slug)
}

func (r *CategoryRepo) Get(ctx context.Context, tenantID uuid.UUID, slug string) (category.Category, error) {
	row, err := r.q.GetCategory(ctx, pgUUID(tenantID), slug)
	if err != nil {
		return category.Category{}, err
	}
	return categoryFromSQLC(row.Category), nil
}

func (r *CategoryRepo) Update(ctx context.Context, args category.UpdateArgs) (category.Category, error) {
	rows, err := r.q.UpdateCategory(ctx,
		pgUUID(args.TenantID),
		args.Slug,
		args.DisplayName,
		args.Description,
		args.Labels,
		args.ExpectedVersion,
	)
	if err != nil {
		return category.Category{}, fmt.Errorf("update category: %w", err)
	}
	if rows == 0 {
		return category.Category{}, category.ErrVersionMismatch
	}
	return r.Get(ctx, args.TenantID, args.Slug)
}

func (r *CategoryRepo) Delete(ctx context.Context, tenantID uuid.UUID, slug string, expectedVersion int64) error {
	rows, err := r.q.DeleteCategory(ctx, pgUUID(tenantID), slug, expectedVersion)
	if err != nil {
		return fmt.Errorf("delete category: %w", err)
	}
	if rows == 0 {
		return category.ErrVersionMismatch
	}
	return nil
}

func (r *CategoryRepo) List(ctx context.Context, tenantID uuid.UUID, pageSize int32, afterSlug string) ([]category.Category, string, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	var after *string
	if afterSlug != "" {
		after = &afterSlug
	}
	rows, err := r.q.ListCategories(ctx, pgUUID(tenantID), after, pageSize)
	if err != nil {
		return nil, "", fmt.Errorf("list categories: %w", err)
	}
	out := make([]category.Category, 0, len(rows))
	for _, row := range rows {
		out = append(out, categoryFromSQLC(row.Category))
	}
	var next string
	if int32(len(out)) == pageSize && len(out) > 0 {
		next = out[len(out)-1].Slug
	}
	return out, next, nil
}

func categoryFromSQLC(c sqlc.Category) category.Category {
	return category.Category{
		TenantID:        uuidFrom(c.TenantID),
		Slug:            c.Slug,
		DisplayName:     derefStr(c.DisplayName),
		Description:     c.Description,
		Labels:          c.Labels,
		ResourceVersion: c.ResourceVersion,
		CreatedAt:       timeFrom(c.CreatedAt),
		UpdatedAt:       timeFrom(c.UpdatedAt),
	}
}
