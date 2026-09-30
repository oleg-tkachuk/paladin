package objecth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ObjectVersion is one immutable history row for a versioned object.
// `IsDeleteMarker == true` represents a tombstone; the object is hidden
// from default reads but ListObjectVersions still surfaces the entry.
type ObjectVersion struct {
	VersionID       uuid.UUID
	ObjectID        uuid.UUID
	IsDeleteMarker  bool
	StoragePath     string
	SizeBytes       int64
	ETag            string
	ChecksumAlgo    string
	Checksum        string
	ContentType     string
	Metadata        map[string]string
	Tags            map[string]string
	LockMode        string // "" | "GOVERNANCE" | "COMPLIANCE"
	LockRetainUntil *time.Time
	LegalHold       bool
	CreatedAt       time.Time
	IsCurrent       bool
}

// VersionRepository persists object_versions rows. Implementations live in
// internal/store/postgres/adapters/object_version.go. The interface is kept
// narrow because the version surface is mostly read-only at the API layer.
type VersionRepository interface {
	// Insert appends a new version row. Used by promotion paths when the
	// parent bucket has versioning enabled.
	Insert(ctx context.Context, v ObjectVersion) error

	// Get returns one version by id. Returns ErrVersionNotFound on miss.
	Get(ctx context.Context, versionID uuid.UUID) (ObjectVersion, error)

	// List returns the immutable history for an object, newest first.
	// Pagination cursor format: "{rfc3339}/{uuid}".
	List(ctx context.Context, objectID uuid.UUID, pageSize int32, pageToken string) ([]ObjectVersion, string, error)

	// CurrentVersionID returns the pointer the parent objects row carries
	// (uuid.Nil if unset, e.g. legacy object created before versioning).
	CurrentVersionID(ctx context.Context, objectID uuid.UUID) (uuid.UUID, error)

	// SetCurrentVersionID flips the pointer atomically. Used by RestoreVersion.
	SetCurrentVersionID(ctx context.Context, objectID, versionID uuid.UUID) error
}

// ErrVersionNotFound mirrors ErrNotFound semantics for version lookups.
var ErrVersionNotFound = errors.New("object version not found")
