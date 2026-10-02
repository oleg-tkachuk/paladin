// Package objecttag implements ObjectTagService business logic. Object tags
// are tenant-scoped taxonomy entries; objects are not FK-linked to them
// (see migration 002).
package objecttagh

import (
	"context"
	"errors"
	"time"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/capability"
)

type ObjectTag struct {
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
	Create(ctx context.Context, args CreateArgs) (ObjectTag, error)
	Get(ctx context.Context, tenantID uuid.UUID, slug string) (ObjectTag, error)
	Update(ctx context.Context, args UpdateArgs) (ObjectTag, error)
	Delete(ctx context.Context, tenantID uuid.UUID, slug string, expectedVersion int64) error
	List(ctx context.Context, tenantID uuid.UUID, pageSize int32, afterSlug string) ([]ObjectTag, string, error)
}

type Handler struct {
	repo Repository
}

func NewHandler(repo Repository) *Handler { return &Handler{repo: repo} }

func (h *Handler) CreateObjectTag(ctx context.Context, args CreateArgs) (*ObjectTag, error) {
	t, err := auth.TenantFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpTag, ""); err != nil {
		return nil, err
	}
	args.TenantID = t
	ot, err := h.repo.Create(ctx, args)
	if err != nil {
		// MapError, not a hardcoded Internal — same reason as
		// collection.CreateCollection: the repository classifies a duplicate
		// slug as ErrObjectTagExists, and forcing Internal here discarded it.
		// Unrecognised errors still land on Internal, MapError's fallback.
		return nil, apiutil.MapError(err)
	}
	return &ot, nil
}

func (h *Handler) GetObjectTag(ctx context.Context, slug string) (*ObjectTag, error) {
	t, err := auth.TenantFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpGet, ""); err != nil {
		return nil, err
	}
	ot, err := h.repo.Get(ctx, t, slug)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return &ot, nil
}

func (h *Handler) UpdateObjectTag(ctx context.Context, args UpdateArgs) (*ObjectTag, error) {
	t, err := auth.TenantFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpTag, ""); err != nil {
		return nil, err
	}
	args.TenantID = t
	ot, err := h.repo.Update(ctx, args)
	if err != nil {
		return nil, apiutil.MapError(err)
	}
	return &ot, nil
}

func (h *Handler) DeleteObjectTag(ctx context.Context, slug string, expectedVersion int64) error {
	t, err := auth.TenantFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpTag, ""); err != nil {
		return err
	}
	if err := h.repo.Delete(ctx, t, slug, expectedVersion); err != nil {
		return apiutil.MapError(err)
	}
	return nil
}

func (h *Handler) ListObjectTags(ctx context.Context, pageSize int32, pageToken string) ([]ObjectTag, string, error) {
	t, err := auth.TenantFromContext(ctx)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeUnauthenticated, err)
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpList, ""); err != nil {
		return nil, "", err
	}
	return h.repo.List(ctx, t, pageSize, pageToken)
}

// ErrObjectTagExists is returned when a Create is refused because the tenant
// already has a tag with that slug — UNIQUE (tenant_id, slug). Same reasoning
// as collection.ErrCollectionExists: a duplicate is an answer, not a fault,
// and it used to arrive as a raw SQLSTATE 23505 under CodeInternal.
var ErrObjectTagExists = errors.New("object tag already exists in this tenant")

var ErrVersionMismatch = errors.New("resource_version mismatch")

// Register this package's sentinels with the central error→Connect-code
// mapper (ADR-0002) so handlers route through apiutil.MapError for a
// consistent code instead of a hand-written per-handler if/else.
func init() {
	apiutil.RegisterError(ErrVersionMismatch, connect.CodeAborted, commonv1.ErrorReason_ERROR_REASON_VERSION_CONFLICT)
	apiutil.RegisterError(ErrObjectTagExists, connect.CodeAlreadyExists, commonv1.ErrorReason_ERROR_REASON_OBJECT_TAG_EXISTS)
}
