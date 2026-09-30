package data

import (
	"context"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/object"
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
	UploadObject(ctx context.Context, in object.UploadObjectInput) (*object.UploadObjectOutput, error)
	CompleteObject(ctx context.Context, in object.CompleteObjectInput) (*object.Object, error)
	ListObjects(ctx context.Context, in object.ListObjectsInput) ([]object.Object, string, error)
	CountObjects(ctx context.Context, in object.CountObjectsInput) (*object.CountObjectsOutput, error)
	GetObject(ctx context.Context, collection, objectID string) (*object.Object, error)
	LookupObject(ctx context.Context, collection, key string) (*object.Object, error)
	DownloadObject(ctx context.Context, collection, objectID string, ttl time.Duration, disposition string) (*object.DownloadObjectOutput, error)
	UpdateObject(ctx context.Context, in object.UpdateObjectInput) (*object.Object, error)
	DeleteObject(ctx context.Context, collection, objectIDStr, resourceVersion string, permanent, bypassGovernance bool) error
	RestoreObject(ctx context.Context, collection, objectIDStr, resourceVersion string) (*object.Object, error)
	CopyObject(ctx context.Context, in object.CopyObjectInput) (*object.Object, error)
}

// Compile-time proof that the production type still fits. Without it the
// interface could drift from the handler and the failure would land at every
// call site instead of here.
var _ objectHandler = (*object.Handler)(nil)

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
	ListVersions(ctx context.Context, in object.ListVersionsInput) ([]object.ObjectVersion, string, error)
	GetVersion(ctx context.Context, name string) (*object.ObjectVersion, error)
	RestoreVersion(ctx context.Context, name, resourceVersion string) (*object.Object, error)
}

type lockHandler interface {
	SetRetention(ctx context.Context, in object.SetRetentionInput) (object.ObjectLock, error)
	SetLegalHold(ctx context.Context, collection, objectID string, hold bool) (object.ObjectLock, error)
	GetLock(ctx context.Context, collection, objectID string) (object.ObjectLock, error)
}

var (
	_ versionHandler = (*object.VersionHandler)(nil)
	_ lockHandler    = (*object.LockHandler)(nil)
)
