// Package bucket holds the bucket domain's shared vocabulary: the row type,
// the repository and provisioner seams the adapters implement, and the
// version-mismatch sentinel.
//
// It used to hold a BucketService handler as well. Nothing constructed it —
// every BucketService RPC is served by api/admin/v1/bucketh — but it read
// like the live implementation, and its DeleteBucket carried a referential
// guard that bucketh did not. That cost a real debugging session: the guard
// was found here, believed, and the delete still reached Postgres and failed
// on collections_bucket_id_fkey. A handler nobody serves is worse than no
// handler, so it is gone; the types stay because wire and the adapters speak
// them.
package bucket

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
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
	CountCollections(ctx context.Context, backendID, bucketName string) (int64, error)
}

var ErrVersionMismatch = errors.New("resource_version mismatch")

// Register this package's sentinels with the central error→Connect-code
// mapper (ADR-0002) so handlers route through apiutil.MapError for a
// consistent code instead of a hand-written per-handler if/else.
func init() {
	apiutil.RegisterError(ErrVersionMismatch, connect.CodeAborted)
}
