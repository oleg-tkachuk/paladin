// Phase 1 of canonical-resource-names: Collection-rooted resources gain
// a canonical form that carries the full (backend, bucket, tenant_id,
// collection) tuple in the resource name. Audit log, event payloads,
// and Cedar resource literals start emitting this shape; the public
// API contract (C-shape `tenants/{tid}/collections/{ok}`) is unchanged
// — connectshim resolvers still accept it.
//
// See backend/docs/canonical-resource-names.md for the broader plan.
package objectkey

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Canonical resource-name shape A:
//
//	storageBackends/{backend_id}/buckets/{bucket_name}/tenants/{tenant_id}/collections/{collection}
//
// `{collection}` may itself be slash-separated (the schema baseline (001_initial_schema.sql): multi-
// segment Collection path) — parsers must NOT split on slash, they
// anchor on the literal `/collections/` separator.
const (
	canonicalBackendPrefix = "storageBackends/"
	canonicalBucketSep     = "/buckets/"
	canonicalTenantSep     = "/tenants/"
	canonicalCollectionSep = "/collections/"
)

// CanonicalName builds the canonical Collection resource name from its
// four components. None of the components may be empty; the function
// panics on misuse rather than emitting a malformed name (it's caller-
// authored at handler level — every site has the components in scope).
func CanonicalName(backendID, bucketName string, tenantID uuid.UUID, collection string) string {
	if backendID == "" || bucketName == "" || tenantID == uuid.Nil || collection == "" {
		panic(fmt.Sprintf(
			"canonical collection: empty component (backend=%q bucket=%q tenant=%s collection=%q)",
			backendID, bucketName, tenantID, collection))
	}
	return canonicalBackendPrefix + backendID +
		canonicalBucketSep + bucketName +
		canonicalTenantSep + tenantID.String() +
		canonicalCollectionSep + collection
}

// CanonicalRef carries the parsed components.
type CanonicalRef struct {
	BackendID  string
	BucketName string
	TenantID   uuid.UUID
	Collection string
}

// ParseCanonical decomposes a canonical-shape resource name. Returns
// an error for any deviation from the exact A-shape — callers that
// also need to accept C-shape ("tenants/{tid}/collections/{ok}") should
// dispatch on prefix BEFORE invoking this. Keeping the parser strict
// makes the shape detector at the connectshim edge unambiguous.
//
// Multi-segment collection paths are preserved verbatim — we anchor on
// `/collections/` and treat everything after as the body.
func ParseCanonical(name string) (CanonicalRef, error) {
	var ref CanonicalRef
	if !strings.HasPrefix(name, canonicalBackendPrefix) {
		return ref, fmt.Errorf("canonical collection: missing %q prefix in %q",
			canonicalBackendPrefix, name)
	}
	rest := name[len(canonicalBackendPrefix):]

	bucketIdx := strings.Index(rest, canonicalBucketSep)
	if bucketIdx <= 0 {
		return ref, fmt.Errorf("canonical collection: missing %q in %q",
			canonicalBucketSep, name)
	}
	ref.BackendID = rest[:bucketIdx]
	rest = rest[bucketIdx+len(canonicalBucketSep):]

	tenantIdx := strings.Index(rest, canonicalTenantSep)
	if tenantIdx <= 0 {
		return ref, fmt.Errorf("canonical collection: missing %q in %q",
			canonicalTenantSep, name)
	}
	ref.BucketName = rest[:tenantIdx]
	rest = rest[tenantIdx+len(canonicalTenantSep):]

	okIdx := strings.Index(rest, canonicalCollectionSep)
	if okIdx <= 0 {
		return ref, fmt.Errorf("canonical collection: missing %q in %q",
			canonicalCollectionSep, name)
	}
	tenantStr := rest[:okIdx]
	tid, err := uuid.Parse(tenantStr)
	if err != nil {
		return ref, fmt.Errorf("canonical collection: tenant_id %q: %w", tenantStr, err)
	}
	ref.TenantID = tid
	ref.Collection = rest[okIdx+len(canonicalCollectionSep):]
	if ref.Collection == "" {
		return ref, fmt.Errorf("canonical collection: empty collection body in %q", name)
	}
	return ref, nil
}

// IsCanonical reports whether `name` parses cleanly as canonical
// shape. Cheap prefix check — does NOT validate UUID format. Used by
// shape-detectors that route between A/C aliases before deciding to
// invoke the full parser.
func IsCanonical(name string) bool {
	return strings.HasPrefix(name, canonicalBackendPrefix) &&
		strings.Contains(name, canonicalBucketSep) &&
		strings.Contains(name, canonicalTenantSep) &&
		strings.Contains(name, canonicalCollectionSep)
}

// TenantPathName is the C-shape resource name `tenants/{tid}/collections/{ok}`.
// Kept here (rather than as a free helper) so the canonical and
// tenant-first forms ship from one place — easier to grep, easier to
// audit when audit-log resource_name semantics shift.
func TenantPathName(tenantID uuid.UUID, collection string) string {
	return "tenants/" + tenantID.String() + "/collections/" + collection
}
