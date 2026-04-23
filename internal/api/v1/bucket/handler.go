// Package bucket implements the BucketService business logic.
//
// Buckets are logical namespaces mapped onto a physical storage backend.
// Create/Update/Delete operations go through Cedar authorization. Delete is
// restricted if any non-DELETED objects still reference the bucket (FK).
package bucket

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

type Bucket struct {
	TenantID        uuid.UUID
	BucketID        string
	DisplayName     string
	StorageBackend  string
	CedarPolicy     string
	LifecycleRules  []byte // JSONB bytes; parsed by caller if needed
	ResourceVersion int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type BucketStats struct {
	ObjectCountAvailable int64
	ObjectCountPending   int64
	ObjectCountDeleted   int64
	SizeBytesAvailable   int64
}

type CreateBucketArgs struct {
	TenantID       uuid.UUID
	BucketID       string
	DisplayName    string
	StorageBackend string
	CedarPolicy    string
	LifecycleRules []byte
}

type UpdateBucketArgs struct {
	TenantID        uuid.UUID
	BucketID        string
	ExpectedVersion int64
	DisplayName     *string
	CedarPolicy     *string
	LifecycleRules  []byte
}

type ListBucketsArgs struct {
	TenantID  uuid.UUID
	PageSize  int32
	PageToken string
}

type Repository interface {
	Create(ctx context.Context, args CreateBucketArgs) (Bucket, error)
	Get(ctx context.Context, tenantID uuid.UUID, bucketID string) (Bucket, error)
	Update(ctx context.Context, args UpdateBucketArgs) (Bucket, error)
	Delete(ctx context.Context, tenantID uuid.UUID, bucketID string, expectedVersion int64) error
	List(ctx context.Context, args ListBucketsArgs) ([]Bucket, string, error)
	Stats(ctx context.Context, tenantID uuid.UUID, bucketID string) (BucketStats, error)
}

type Handler struct {
	repo   Repository
	policy *cedar.Engine
}

func NewHandler(repo Repository, policy *cedar.Engine) *Handler {
	return &Handler{repo: repo, policy: policy}
}

func (h *Handler) CreateBucket(ctx context.Context, args CreateBucketArgs) (*Bucket, error) {
	tenantID, principal, err := authzContext(ctx)
	if err != nil {
		return nil, err
	}
	args.TenantID = tenantID
	if err := h.authorize(ctx, principal, tenantID, args.BucketID, cedar.ActionAdminBucket); err != nil {
		return nil, err
	}
	b, err := h.repo.Create(ctx, args)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create bucket: %w", err))
	}
	return &b, nil
}

func (h *Handler) GetBucket(ctx context.Context, bucketID string) (*Bucket, error) {
	tenantID, principal, err := authzContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, principal, tenantID, bucketID, cedar.ActionAdminBucket); err != nil {
		return nil, err
	}
	b, err := h.repo.Get(ctx, tenantID, bucketID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return &b, nil
}

func (h *Handler) UpdateBucket(ctx context.Context, args UpdateBucketArgs) (*Bucket, error) {
	tenantID, principal, err := authzContext(ctx)
	if err != nil {
		return nil, err
	}
	args.TenantID = tenantID
	if err := h.authorize(ctx, principal, tenantID, args.BucketID, cedar.ActionAdminBucket); err != nil {
		return nil, err
	}
	b, err := h.repo.Update(ctx, args)
	if err != nil {
		return nil, mapVersionErr(err)
	}
	return &b, nil
}

func (h *Handler) DeleteBucket(ctx context.Context, bucketID string, expectedVersion int64) error {
	tenantID, principal, err := authzContext(ctx)
	if err != nil {
		return err
	}
	if err := h.authorize(ctx, principal, tenantID, bucketID, cedar.ActionAdminBucket); err != nil {
		return err
	}
	if err := h.repo.Delete(ctx, tenantID, bucketID, expectedVersion); err != nil {
		return mapVersionErr(err)
	}
	return nil
}

func (h *Handler) ListBuckets(ctx context.Context, args ListBucketsArgs) ([]Bucket, string, error) {
	tenantID, _, err := authzContext(ctx)
	if err != nil {
		return nil, "", err
	}
	args.TenantID = tenantID
	// ListBuckets is permitted for any authenticated tenant member; per-bucket
	// visibility is not filtered through Cedar here to keep pagination cheap.
	return h.repo.List(ctx, args)
}

func (h *Handler) GetBucketStats(ctx context.Context, bucketID string) (*BucketStats, error) {
	tenantID, principal, err := authzContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, principal, tenantID, bucketID, cedar.ActionAdminBucket); err != nil {
		return nil, err
	}
	s, err := h.repo.Stats(ctx, tenantID, bucketID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &s, nil
}

func authzContext(ctx context.Context) (uuid.UUID, *auth.Principal, error) {
	tenantID, err := auth.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return uuid.Nil, nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	return tenantID, p, nil
}

func (h *Handler) authorize(ctx context.Context, p *auth.Principal, tenantID uuid.UUID, bucketID, action string) error {
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{Subject: p.Subject, TenantID: tenantID, Roles: p.Roles},
		action,
		&cedar.Resource{TenantID: tenantID, BucketID: bucketID},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))
	}
	return nil
}

// ErrVersionMismatch is returned when optimistic-concurrency control fails.
// Repositories should surface it so handlers can map to CodeAborted.
var ErrVersionMismatch = errors.New("resource_version mismatch")

func mapVersionErr(err error) error {
	if errors.Is(err, ErrVersionMismatch) {
		return connect.NewError(connect.CodeAborted, err)
	}
	return connect.NewError(connect.CodeInternal, err)
}
