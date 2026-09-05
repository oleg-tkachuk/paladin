package data

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/batch"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/multipart"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/operation"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/presign"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/storagebootstrap"
)

// The remaining shims' seams, same shape and same reason as object_seam.go:
// each server held a concrete *Handler, so its error branches could only be
// reached by standing up a real handler with a database behind it.
//
// One interface per server rather than one per handler TYPE. ObjectTagServer
// and ObjectServer both hold an *object.Handler and call different parts of
// it; a shared interface would make each server declare a dependency on
// methods it never touches, and would stop shrinking when a shim drops a call.
//
// Constructors keep taking concrete types wherever a nil is possible — see
// NewObjectServer for the typed-nil trap that costs, which is a panic rather
// than a compile error.

type batchHandler interface {
	BatchDelete(ctx context.Context, args batch.BatchDeleteArgs) (uuid.UUID, error)
	BatchCopy(ctx context.Context, args batch.BatchCopyArgs) (uuid.UUID, error)
	BatchRestoreObjects(ctx context.Context, args batch.BatchRestoreObjectsArgs) (uuid.UUID, error)
	BatchUpdateTags(ctx context.Context, args batch.BatchUpdateTagsArgs) (uuid.UUID, error)
}

type multipartHandler interface {
	InitiateMultipartUpload(ctx context.Context, args multipart.InitiateArgs) (*multipart.Session, error)
	PresignPart(ctx context.Context, uploadID string, partNumber int32, ttl time.Duration, want multipart.SessionRef) (string, map[string]string, time.Time, error)
	ListParts(ctx context.Context, uploadID string, pageSize int32, pageToken string, want multipart.SessionRef) ([]multipart.Part, string, error)
	CompleteMultipartUpload(ctx context.Context, args multipart.CompleteArgs) error
	AbortMultipartUpload(ctx context.Context, uploadID string, want multipart.SessionRef) error
}

type objectTagHandler interface {
	GetObject(ctx context.Context, collection, objectID string) (*object.Object, error)
	UpdateObject(ctx context.Context, in object.UpdateObjectInput) (*object.Object, error)
	ListDistinctTags(ctx context.Context, collection, pageToken string, pageSize int32) (object.DistinctTagPage, error)
}

type presignHandler interface {
	PresignGet(ctx context.Context, collection, objectIDStr string, ttl time.Duration, disposition string) (string, map[string]string, time.Time, error)
	PresignPut(ctx context.Context, collection, objectIDStr, contentType, checksumAlgo string, ttl time.Duration, sizeHint int64) (string, map[string]string, time.Time, error)
}

type operationHandler interface {
	GetOperation(ctx context.Context, opID uuid.UUID) (*operation.Operation, error)
	ListOperations(ctx context.Context, state *operation.State, pageSize int32, pageToken, filter string, newestFirst bool) ([]operation.Operation, string, error)
	CancelOperation(ctx context.Context, opID uuid.UUID) error
}

type storageBootstrapHandler interface {
	EnsureTenantStorage(ctx context.Context, backendID, bucket string, collections []string) (*storagebootstrap.Result, error)
}

// Compile-time proof that each production type still fits. Without these the
// interface and the handler drift apart at every call site instead of here.
var (
	_ batchHandler            = (*batch.Handler)(nil)
	_ multipartHandler        = (*multipart.Handler)(nil)
	_ objectTagHandler        = (*object.Handler)(nil)
	_ presignHandler          = (*presign.Handler)(nil)
	_ operationHandler        = (*operation.Handler)(nil)
	_ storageBootstrapHandler = (*storagebootstrap.Handler)(nil)
)
