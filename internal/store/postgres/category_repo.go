package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	apperrors "github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// CategoryRepo implements domain.CategoryRepository backed by PostgreSQL.
type CategoryRepo struct {
	db *DB
}

func NewCategoryRepo(db *DB) *CategoryRepo {
	return &CategoryRepo{db: db}
}

func (r *CategoryRepo) Create(ctx context.Context, rec domain.Category) error {
	err := r.db.Queries.CreateCategory(ctx,
		uuidToPgtype(rec.ID),
		rec.TenantID,
		rec.Slug,
		rec.Name,
		rec.Description,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return apperrors.Conflict(fmt.Sprintf("category %q already exists", rec.Slug), nil)
		}
		return fmt.Errorf("create category: %w", err)
	}
	return nil
}

func (r *CategoryRepo) Get(ctx context.Context, tenantID, slug string) (*domain.Category, error) {
	row, err := r.db.Queries.GetCategory(ctx, tenantID, slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperrors.NotFound(fmt.Sprintf("category %q not found", slug), nil)
		}
		return nil, fmt.Errorf("get category: %w", err)
	}
	cat := mapSqlcCategoryToDomain(row)
	return &cat, nil
}

func (r *CategoryRepo) List(ctx context.Context, tenantID string, limit int, cursor string) ([]domain.Category, string, error) {
	var pgCursor pgtype.Timestamptz
	if cursor != "" {
		t, err := time.Parse(time.RFC3339, cursor)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}
		pgCursor = timestampToPgtype(t)
	}

	rows, err := r.db.Queries.ListCategories(ctx, tenantID, int32(limit+1), pgCursor)
	if err != nil {
		return nil, "", fmt.Errorf("list categories: %w", err)
	}

	cats := make([]domain.Category, 0, len(rows))
	for _, row := range rows {
		cats = append(cats, mapSqlcCategoryToDomain(row))
	}

	nextCursor := ""
	if len(cats) > limit {
		nextCursor = cats[limit-1].CreatedAt.Format(time.RFC3339)
		cats = cats[:limit]
	}

	return cats, nextCursor, nil
}

func (r *CategoryRepo) Delete(ctx context.Context, tenantID, slug string) (bool, error) {
	n, err := r.db.Queries.DeleteCategory(ctx, tenantID, slug)
	if err != nil {
		return false, fmt.Errorf("delete category: %w", err)
	}
	return n > 0, nil
}

func (r *CategoryRepo) Exists(ctx context.Context, tenantID, slug string) (bool, error) {
	exists, err := r.db.Queries.CategoryExists(ctx, tenantID, slug)
	if err != nil {
		return false, fmt.Errorf("category exists check: %w", err)
	}
	return exists, nil
}

func (r *CategoryRepo) ObjectCount(ctx context.Context, tenantID, slug string) (int64, error) {
	count, err := r.db.Queries.CategoryObjectCount(ctx, tenantID, slug)
	if err != nil {
		return 0, fmt.Errorf("category object count: %w", err)
	}
	return count, nil
}

func (r *CategoryRepo) ListTenants(ctx context.Context) ([]string, error) {
	tenants, err := r.db.Queries.ListTenants(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	return tenants, nil
}

// mapSqlcCategoryToDomain converts a sqlc.ObjectCategory row to domain.Category.
func mapSqlcCategoryToDomain(row sqlc.ObjectCategory) domain.Category {
	id := uuid.UUID(row.ID.Bytes)
	return domain.Category{
		ID:          id,
		TenantID:    row.TenantID,
		Slug:        row.Slug,
		Name:        row.Name,
		Description: row.Description,
		CreatedAt:   timestampFromPgtype(row.CreatedAt),
		UpdatedAt:   timestampFromPgtype(row.UpdatedAt),
	}
}
