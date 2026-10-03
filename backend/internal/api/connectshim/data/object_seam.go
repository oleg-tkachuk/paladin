package data

import (
	"context"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
)

// objectHandler is what ObjectServer needs from the domain handler, and the
// reason it exists is testability rather than abstraction.
//
// The field was a concrete *object.Handler, so the shim's error branches could
// not be reached from a test: standing up a real handler needs a repo, a
// storage client and a database. A mutation run made that concrete — inverting
// `if err != nil` after any of these calls survived the WHOLE non-stack unit
// suite, because nothing below the slow gate ever drove an error through this
// layer.
//
// Declared here rather than beside the handler on purpose: the consumer names
// what it needs, so this list is the shim's actual surface and shrinks when
// the shim stops calling something. *object.Handler satisfies it implicitly,
// so no caller of NewObjectServer changed.
type objectHandler interface {
	UploadObject(ctx context.Context, in objecth.UploadObjectInput) (*objecth.UploadObjectOutput, error)
	CompleteObject(ctx context.Context, in objecth.CompleteObjectInput) (*objecth.Object, error)
	ListObjects(ctx context.Context, in objecth.ListObjectsInput) ([]objecth.Object, string, error)
	CountObjects(ctx context.Context, in objecth.CountObjectsInput) (*objecth.CountObjectsOutput, error)
	GetObject(ctx context.Context, collection, objectID string) (*objecth.Object, error)
	LookupObject(ctx context.Context, collection, key string) (*objecth.Object, error)
	DownloadObject(ctx context.Context, collection, objectID string, ttl time.Duration, disposition string, requireETagMatch bool) (*objecth.DownloadObjectOutput, error)
	UpdateObject(ctx context.Context, in objecth.UpdateObjectInput) (*objecth.Object, error)
	DeleteObject(ctx context.Context, collection, objectIDStr, resourceVersion string, permanent, bypassGovernance bool) error
	RestoreObject(ctx context.Context, collection, objectIDStr, resourceVersion string) (*objecth.Object, error)
	CopyObject(ctx context.Context, in objecth.CopyObjectInput) (*objecth.Object, error)
}

// Compile-time proof that the production type still fits. Without it the
// interface could drift from the handler and the failure would land at every
// call site instead of here.
var _ objectHandler = (*objecth.Handler)(nil)

// versionHandler and lockHandler are the same seam for the two optional fields
// on ObjectServer. Both stay nil in a deployment that has not enabled the
// feature, and the shim answers Unimplemented — so the interface value being
// nil is a supported state, not a wiring bug.
//
// Split into three interfaces rather than one because the fields are three
// separate handlers wired independently. A single combined interface would
// force a test double to implement methods it has no business knowing about,
// and would make `Locks == nil` unrepresentable.
type versionHandler interface {
	ListVersions(ctx context.Context, in objecth.ListVersionsInput) ([]objecth.ObjectVersion, string, error)
	GetVersion(ctx context.Context, name string) (*objecth.ObjectVersion, error)
	RestoreVersion(ctx context.Context, name, resourceVersion string) (*objecth.Object, error)
}

type lockHandler interface {
	SetRetention(ctx context.Context, in objecth.SetRetentionInput) (objecth.ObjectLock, error)
	SetLegalHold(ctx context.Context, collection, objectID string, hold bool) (objecth.ObjectLock, error)
	GetLock(ctx context.Context, collection, objectID string) (objecth.ObjectLock, error)
}

type taintHandler interface {
	SetTaint(ctx context.Context, collection, objectID string, signals []string) (*objecth.Object, error)
}

var (
	_ versionHandler = (*objecth.VersionHandler)(nil)
	_ lockHandler    = (*objecth.LockHandler)(nil)
	_ taintHandler   = (*objecth.TaintHandler)(nil)
)
