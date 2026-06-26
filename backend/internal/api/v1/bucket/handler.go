// Package bucket implements BucketService — provisioning physical S3 buckets
// inside configured storage backends. Hierarchy: backend → bucket → object_key.
package bucket

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
)

type Bucket struct {
	BackendID       string
	BucketName      string
	DisplayName     string
	Region          string
	Labels          []byte // JSONB
	ResourceVersion int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type CreateArgs struct {
	BackendID   string
	BucketName  string
	DisplayName string
	Region      string
	Labels      []byte
}

type UpdateArgs struct {
	BackendID       string
	BucketName      string
	ExpectedVersion int64
	DisplayName     *string
	Labels          []byte
}

type ListArgs struct {
	BackendID *string // optional filter
	PageSize  int32
	PageToken string
}

// Provisioner abstracts the AWS-side CreateBucket call so handler tests can
// fake it. Implementations live in s3adapter.
type Provisioner interface {
	// CreateBucket provisions a real S3 bucket on the backend. Returns nil
	// if the bucket already exists (idempotent).
	CreateBucket(ctx context.Context, backendID, bucketName, region string) error
	// DeleteBucket removes the real S3 bucket. Optional — BucketService
	// only calls this when delete_remote=true.
	DeleteBucket(ctx context.Context, backendID, bucketName string) error
}

type Repository interface {
	Create(ctx context.Context, args CreateArgs) (Bucket, error)
	Get(ctx context.Context, backendID, bucketName string) (Bucket, error)
	Update(ctx context.Context, args UpdateArgs) (Bucket, error)
	Delete(ctx context.Context, backendID, bucketName string, expectedVersion int64) error
	List(ctx context.Context, args ListArgs) ([]Bucket, string, error)
	CountObjectKeys(ctx context.Context, backendID, bucketName string) (int64, error)
}

type Handler struct {
	repo           Repository
	provisioner    Provisioner
	defaultBackend string
}

func NewHandler(repo Repository, provisioner Provisioner, defaultBackend string) *Handler {
	return &Handler{repo: repo, provisioner: provisioner, defaultBackend: defaultBackend}
}

func (h *Handler) CreateBucket(ctx context.Context, args CreateArgs) (*Bucket, error) {
	if _, err := auth.PrincipalFromContext(ctx); err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	if args.BackendID == "" {
		args.BackendID = h.defaultBackend
	}
	if args.BackendID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("backend_id required and no default configured"))
	}
	// Provision physical S3 bucket first; CreateBucket is idempotent so a
	// retry after a partial failure is safe.
	if h.provisioner != nil {
		if err := h.provisioner.CreateBucket(ctx, args.BackendID, args.BucketName, args.Region); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("provision bucket: %w", err))
		}
	}
	b, err := h.repo.Create(ctx, args)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("record bucket: %w", err))
	}
	return &b, nil
}

func (h *Handler) GetBucket(ctx context.Context, backendID, bucketName string) (*Bucket, error) {
	if _, err := auth.PrincipalFromContext(ctx); err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	b, err := h.repo.Get(ctx, backendID, bucketName)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return &b, nil
}

func (h *Handler) UpdateBucket(ctx context.Context, args UpdateArgs) (*Bucket, error) {
	if _, err := auth.PrincipalFromContext(ctx); err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	b, err := h.repo.Update(ctx, args)
	if err != nil {
		return nil, apiutil.MapError(err)
	}
	return &b, nil
}

func (h *Handler) DeleteBucket(ctx context.Context, backendID, bucketName string, expectedVersion int64, deleteRemote bool) error {
	if _, err := auth.PrincipalFromContext(ctx); err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	// Refuse if any ObjectKey still uses this bucket.
	count, err := h.repo.CountObjectKeys(ctx, backendID, bucketName)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	if count > 0 {
		return connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("bucket has %d ObjectKey references; remove them first", count))
	}
	if err := h.repo.Delete(ctx, backendID, bucketName, expectedVersion); err != nil {
		return apiutil.MapError(err)
	}
	if deleteRemote && h.provisioner != nil {
		if err := h.provisioner.DeleteBucket(ctx, backendID, bucketName); err != nil {
			// Log via error wrapping; the row is already gone, so the caller
			// will need to clean up the dangling S3 bucket out-of-band.
			return connect.NewError(connect.CodeInternal,
				fmt.Errorf("DB row deleted but S3 DeleteBucket failed: %w", err))
		}
	}
	return nil
}

func (h *Handler) ListBuckets(ctx context.Context, args ListArgs) ([]Bucket, string, error) {
	if _, err := auth.PrincipalFromContext(ctx); err != nil {
		return nil, "", connect.NewError(connect.CodeUnauthenticated, err)
	}
	return h.repo.List(ctx, args)
}

var ErrVersionMismatch = errors.New("resource_version mismatch")

// Register this package's sentinels with the central error→Connect-code
// mapper (ADR-0002) so handlers route through apiutil.MapError for a
// consistent code instead of a hand-written per-handler if/else.
func init() {
	apiutil.RegisterError(ErrVersionMismatch, connect.CodeAborted)
}
