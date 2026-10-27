// Package storagepath is the one place an object's key inside its bucket is
// composed. The S3 adapter writes and reads bytes at this path, and the data
// plane reports it to callers allowed to see an object's physical placement
// (Object.placement), so the two cannot disagree on where an object lives.
package storagepath

import (
	"strings"
	"uuid"
)

// Compose builds the full bucket key from tenant_id + collection + storage key.
// Layout: "<tenant_id>/<collection>/<key>". Both prefix segments are required;
// callers must supply them. The leading slash is omitted (S3 keys never start
// with one).
//
// Defense-in-depth: collection/key are server-generated and validated
// upstream today, so this is a belt-and-suspenders guard, not a live fix.
// S3 keys are opaque (it doesn't interpret "..") but a future
// filesystem-backed adapter or any tenant-prefix access check would be
// fooled by a "../other-tenant" segment, so we strip path-traversal
// components. sanitizeSegment is a no-op for clean inputs (UUIDs,
// validated keys).
func Compose(tenantID uuid.UUID, collection, key string) string {
	return tenantID.String() + "/" + sanitizeSegment(collection) + "/" + sanitizeSegment(key)
}

// sanitizeSegment removes leading slashes and any "." / ".." path
// components so a segment can never escape its parent prefix. The
// segment's internal "/" structure is otherwise preserved (multi-level
// object keys are legal).
func sanitizeSegment(s string) string {
	if s == "" {
		return s
	}
	parts := strings.Split(s, "/")
	out := parts[:0]
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, "/")
}
