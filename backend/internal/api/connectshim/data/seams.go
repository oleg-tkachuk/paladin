package data

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/batchh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/multiparth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/presignh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/storagebootstraph"
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
	BatchDelete(ctx context.Context, args batchh.BatchDeleteArgs) (uuid.UUID, error)
	BatchCopy(ctx context.Context, args batchh.BatchCopyArgs) (uuid.UUID, error)
	BatchRestoreObjects(ctx context.Context, args batchh.BatchRestoreObjectsArgs) (uuid.UUID, error)
	BatchUpdateTags(ctx context.Context, args batchh.BatchUpdateTagsArgs) (uuid.UUID, error)
}

type multipartHandler interface {
	InitiateMultipartUpload(ctx context.Context, args multiparth.InitiateArgs) (*multiparth.Session, error)
	PresignPart(ctx context.Context, uploadID string, partNumber int32, ttl time.Duration, want multiparth.SessionRef) (string, map[string]string, time.Time, error)
	ListParts(ctx context.Context, uploadID string, pageSize int32, pageToken string, want multiparth.SessionRef) ([]multiparth.Part, string, error)
	CompleteMultipartUpload(ctx context.Context, args multiparth.CompleteArgs) error
	AbortMultipartUpload(ctx context.Context, uploadID string, want multiparth.SessionRef) error
}

type objectTagHandler interface {
	GetObject(ctx context.Context, collection, objectID string) (*objecth.Object, error)
	UpdateObject(ctx context.Context, in objecth.UpdateObjectInput) (*objecth.Object, error)
	ListDistinctTags(ctx context.Context, collection, pageToken string, pageSize int32) (objecth.DistinctTagPage, error)
}

type presignHandler interface {
	PresignGet(ctx context.Context, collection, objectIDStr string, ttl time.Duration, disposition string) (string, map[string]string, time.Time, error)
	RegenerateUploadURL(ctx context.Context, collection, objectIDStr string, ttl time.Duration) (presignh.UploadURL, error)
}

type operationHandler interface {
	GetOperation(ctx context.Context, opID uuid.UUID) (*operationh.Operation, error)
	ListOperations(ctx context.Context, state *operationh.State, pageSize int32, pageToken, filter string, newestFirst bool) ([]operationh.Operation, string, error)
	CancelOperation(ctx context.Context, opID uuid.UUID) error
}

type storageBootstrapHandler interface {
	EnsureTenantStorage(ctx context.Context, backendID, bucket string, collections []string) (*storagebootstraph.Result, error)
}

// Compile-time proof that each production type still fits. Without these the
// interface and the handler drift apart at every call site instead of here.
var (
	_ batchHandler            = (*batchh.Handler)(nil)
	_ multipartHandler        = (*multiparth.Handler)(nil)
	_ objectTagHandler        = (*objecth.Handler)(nil)
	_ presignHandler          = (*presignh.Handler)(nil)
	_ operationHandler        = (*operationh.Handler)(nil)
	_ storageBootstrapHandler = (*storagebootstraph.Handler)(nil)
)
