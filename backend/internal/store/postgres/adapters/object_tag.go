package adapters

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	objecttag "github.com/oleg-tkachuk/paladin/internal/api/v1/object_tag"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/pgerr"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/schema"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// ObjectTagRepo satisfies objecttag.Repository.
type ObjectTagRepo struct {
	q *sqlc.Queries
}

func NewObjectTagRepo(q *sqlc.Queries) *ObjectTagRepo { return &ObjectTagRepo{q: q} }

var _ objecttag.Repository = (*ObjectTagRepo)(nil)

func (r *ObjectTagRepo) Create(ctx context.Context, args objecttag.CreateArgs) (objecttag.ObjectTag, error) {
	// object_tags.labels is JSONB NOT NULL DEFAULT '{}'. The INSERT binds it
	// explicitly, so a nil []byte becomes SQL NULL and violates the
	// constraint. Normalize to an empty JSON object.
	labels := args.Labels
	if len(labels) == 0 {
		labels = []byte("{}")
	}
	if err := r.q.CreateObjectTag(ctx,
		pgUUID(args.TenantID),
		args.Slug,
		strPtr(args.DisplayName),
		args.Description,
		labels,
	); err != nil {
		// Same shape as CollectionRepo.createWith: a slug the tenant already
		// uses is a duplicate, not a fault, and unmapped it reached the caller
		// as CodeInternal carrying the raw SQLSTATE 23505.
		if pgerr.Is(err, pgerr.UniqueViolation) &&
			pgerr.ConstraintIs(err, schema.ObjectTagsSlugUnique) {
			return objecttag.ObjectTag{}, fmt.Errorf("%w: %q",
				objecttag.ErrObjectTagExists, args.Slug)
		}
		return objecttag.ObjectTag{}, fmt.Errorf("create object tag: %w", err)
	}
	return r.Get(ctx, args.TenantID, args.Slug)
}

func (r *ObjectTagRepo) Get(ctx context.Context, tenantID uuid.UUID, slug string) (objecttag.ObjectTag, error) {
	row, err := r.q.GetObjectTag(ctx, pgUUID(tenantID), slug)
	if err != nil {
		return objecttag.ObjectTag{}, err
	}
	return ObjectTagFromSQLC(row.ObjectTag), nil
}

func (r *ObjectTagRepo) Update(ctx context.Context, args objecttag.UpdateArgs) (objecttag.ObjectTag, error) {
	rows, err := r.q.UpdateObjectTag(ctx,
		pgUUID(args.TenantID),
		args.Slug,
		args.DisplayName,
		args.Description,
		args.Labels,
		args.ExpectedVersion,
	)
	if err != nil {
		return objecttag.ObjectTag{}, fmt.Errorf("update object tag: %w", err)
	}
	if rows == 0 {
		return objecttag.ObjectTag{}, objecttag.ErrVersionMismatch
	}
	return r.Get(ctx, args.TenantID, args.Slug)
}

func (r *ObjectTagRepo) Delete(ctx context.Context, tenantID uuid.UUID, slug string, expectedVersion int64) error {
	rows, err := r.q.DeleteObjectTag(ctx, pgUUID(tenantID), slug, expectedVersion)
	if err != nil {
		return fmt.Errorf("delete object tag: %w", err)
	}
	if rows == 0 {
		return objecttag.ErrVersionMismatch
	}
	return nil
}

func (r *ObjectTagRepo) List(ctx context.Context, tenantID uuid.UUID, pageSize int32, afterSlug string) ([]objecttag.ObjectTag, string, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	var after *string
	if afterSlug != "" {
		after = &afterSlug
	}
	rows, err := r.q.ListObjectTags(ctx, pgUUID(tenantID), after, pageSize)
	if err != nil {
		return nil, "", fmt.Errorf("list object tags: %w", err)
	}
	out := make([]objecttag.ObjectTag, 0, len(rows))
	for _, row := range rows {
		out = append(out, ObjectTagFromSQLC(row.ObjectTag))
	}
	var next string
	if len(out) == int(pageSize) && len(out) > 0 {
		next = out[len(out)-1].Slug
	}
	return out, next, nil
}

func ObjectTagFromSQLC(c sqlc.ObjectTag) objecttag.ObjectTag {
	return objecttag.ObjectTag{
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
