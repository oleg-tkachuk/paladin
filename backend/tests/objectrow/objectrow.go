// Package objectrow inserts object rows for tests that seed the objects table
// by hand, placed and anchored as Paladin's own writers leave them
// (ADR-0051): a PENDING row anchored to the bucket its URL is signed for
// (objects.upload_bucket_id), any other row placed in the bucket its bytes
// are in (objects.placement_bucket_id). The schema refuses a row without them
// (migration 132), so a fixture that skipped them would fail to insert rather
// than test a row no writer makes.
//
// A seed of many rows in one INSERT … SELECT FROM generate_series sets the
// columns in its own SQL, from the collection's binding as Insert does:
// placement_bucket_id for a state past PENDING, upload_bucket_id for PENDING.
package objectrow

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
)

// The object states a fixture names, as object_state spells them.
const (
	StatePending   = "PENDING"
	StateAvailable = "AVAILABLE"
	StateDeleted   = "DELETED"
)

// DefaultContentType is the content type of a row that names none.
const DefaultContentType = "application/octet-stream"

// Querier is what Insert runs on: a pool, a connection or a transaction.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Row is one object to insert. Zero values mean the column's default, except
// where a field says otherwise.
type Row struct {
	// ID is the row's id; zero inserts a new one.
	ID       uuid.UUID
	TenantID uuid.UUID
	// The collection, by id or, when CollectionID is zero, by name under
	// TenantID.
	CollectionID uuid.UUID
	Collection   string
	Path         string
	// State is the row's object_state; empty is StateAvailable.
	State string
	// ContentType is empty for DefaultContentType.
	ContentType       string
	ChecksumAlgorithm int16
	Checksum          *string
	SizeBytes         *int64
	ETag              *string
	// Tags and Metadata are JSON objects; empty is the column's default.
	Tags     string
	Metadata string
	// The row's timestamps; nil leaves each NULL, or for CreatedAt the
	// column's default.
	CreatedAt        *time.Time
	CommittedAt      *time.Time
	TerminatedAt     *time.Time
	PresignExpiresAt *time.Time
	BytesWrittenAt   *time.Time
	// CreatedByOperation is the operation that created the row; zero for
	// none.
	CreatedByOperation uuid.UUID
	// Bucket is where the row is placed, or anchored while PENDING; zero is
	// the collection's binding.
	Bucket uuid.UUID
	// Unsettled keeps the anchor of a row past PENDING: its URL can still
	// write, and the reconciler has yet to settle it.
	Unsettled bool
}

// ErrNoCollection is a Row whose collection matched no row.
var ErrNoCollection = errors.New("objectrow: no such collection")

// insertSQL inserts one row, its collection resolved by id or by name under
// the tenant, its anchor and placement following its state.
const insertSQL = `
INSERT INTO objects (
    id, tenant_id, collection_id, path, state, content_type, checksum_algorithm,
    checksum, size_bytes, etag, tags, metadata, created_at, committed_at,
    terminated_at, presign_expires_at, bytes_written_at, created_by_operation,
    upload_bucket_id, placement_bucket_id)
SELECT $1, c.tenant_id, c.id, $4, $5::object_state, $6, $7,
       $8, $9, $10, COALESCE($11::jsonb, '{}'::jsonb), COALESCE($12::jsonb, '{}'::jsonb),
       COALESCE($13::timestamptz, now()), $14,
       $15, $16, $17, $18,
       CASE WHEN $5::object_state = 'PENDING' OR $20::bool THEN COALESCE($19::uuid, c.bucket_id) END,
       CASE WHEN $5::object_state <> 'PENDING' THEN COALESCE($19::uuid, c.bucket_id) END
  FROM collections c
 WHERE c.tenant_id = $2
   AND (($3::uuid IS NOT NULL AND c.id = $3::uuid) OR ($3::uuid IS NULL AND c.name = $21))
RETURNING id`

// Insert inserts r on q and returns its id.
func Insert(ctx context.Context, q Querier, r Row) (uuid.UUID, error) {
	id := r.ID
	if id == (uuid.UUID{}) {
		id = uuid.NewV7()
	}
	state := r.State
	if state == "" {
		state = StateAvailable
	}
	contentType := r.ContentType
	if contentType == "" {
		contentType = DefaultContentType
	}
	var got uuid.UUID
	err := q.QueryRow(ctx, insertSQL,
		id, r.TenantID, optionalID(r.CollectionID), r.Path, state, contentType, r.ChecksumAlgorithm,
		r.Checksum, r.SizeBytes, r.ETag, optionalText(r.Tags), optionalText(r.Metadata),
		r.CreatedAt, r.CommittedAt,
		r.TerminatedAt, r.PresignExpiresAt, r.BytesWrittenAt, optionalID(r.CreatedByOperation),
		optionalID(r.Bucket), r.Unsettled, r.Collection,
	).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, fmt.Errorf("%w: tenant %s, collection %q (id %s)", ErrNoCollection, r.TenantID, r.Collection, r.CollectionID)
	}
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("objectrow: insert %q: %w", r.Path, err)
	}
	return got, nil
}

// optionalID is id, or NULL for the zero id.
func optionalID(id uuid.UUID) *uuid.UUID {
	if id == (uuid.UUID{}) {
		return nil
	}
	return &id
}

// optionalText is s, or NULL for the empty string.
func optionalText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Ptr is a pointer to v, for the Row fields a fixture sets inline.
func Ptr[T any](v T) *T {
	return &v
}
