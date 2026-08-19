// Package resolve centralises object_key / tenant resource-name parsing for
// the connectshim edge. Phase 2 of backend/docs/canonical-resource-names.md:
// clients may send any of three name shapes, and every handler that needs the
// (tenant, object_key) tuple goes through one resolver instead of each
// re-implementing the anchor-on-prefix parse.
package resolve

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin-private/internal/auth"
	"github.com/oleg-tkachuk/paladin-private/internal/metrics"
)

// Shape classifies which of the three resource-name forms a caller sent.
type Shape string

const (
	// ShapeCanonical — A: storageBackends/{b}/buckets/{bk}/tenants/{t}/objectKeys/{ok}
	ShapeCanonical Shape = "canonical"
	// ShapeTenant — C: tenants/{t}/objectKeys/{ok}
	ShapeTenant Shape = "tenant"
	// ShapeBare — B: {ok}; tenant taken from the caller's auth context.
	ShapeBare Shape = "bare"
)

// CanonicalRef is the resolved object_key reference. BackendID / BucketName
// are populated when the canonical (A) shape carried them, or when a bare (B)
// name was enriched via ResolveObjectKeyNameWithBinding.
type CanonicalRef struct {
	TenantID   uuid.UUID
	ObjectKey  string
	BackendID  string
	BucketName string
	Shape      Shape
}

// DefaultBindingLookup resolves a tenant's default (backend, bucket) route,
// used to complete the bare (B) object_key shape to canonical. Implemented by
// the tenant store adapter (ADR-0010 Phase 3 / migration 034).
type DefaultBindingLookup interface {
	TenantDefaultBinding(ctx context.Context, tenantID uuid.UUID) (backendID, bucketName string, found bool, err error)
}

// ErrNoDefaultBinding is returned when a bare (B) object_key name is used but
// the caller's tenant has no default binding set. Callers map it to
// CodeFailedPrecondition (structured reason NO_DEFAULT_BINDING).
var ErrNoDefaultBinding = errors.New("bare object_key name requires a tenant default binding; none is set")

// The shape distribution is recorded via metrics.RecordResourceNameShape so
// it flows over the OTLP pipeline Paladin actually exports — the previous
// prometheus default-registry counter was never served, so it collected no
// observable data (Phase 3 needs the real distribution to decide on
// deprecating a shape).

const (
	prefixCanonical = "storageBackends/"
	prefixTenant    = "tenants/"
	objectKeysSep   = "/objectKeys/"
)

// ResolveObjectKeyName parses any of the three object_key name shapes into a
// CanonicalRef and records the shape. `ok` may contain '/'. The bare (B)
// shape carries no tenant in the name, so the caller's tenant is read from
// ctx.
//
// This is the entry point for the connectshim edge — exactly one shape
// observation per inbound request. Interceptors and other in-process
// readers that need the same parse must call ParseObjectKeyName instead, or
// the shape distribution (which Phase 3 uses to decide whether a shape can
// be deprecated) would count the same request twice.
func ResolveObjectKeyName(ctx context.Context, name string) (CanonicalRef, error) {
	ref, err := ParseObjectKeyName(ctx, name)
	if err != nil {
		return CanonicalRef{}, err
	}
	metrics.RecordResourceNameShape(ctx, string(ref.Shape))
	return ref, nil
}

// ParseObjectKeyName is ResolveObjectKeyName without the metric. Use it when
// something other than the request edge needs the tuple — the quota
// interceptor re-parses the same name the shim will parse, and both
// recording would double-count the shape.
func ParseObjectKeyName(ctx context.Context, name string) (CanonicalRef, error) {
	if name == "" {
		return CanonicalRef{}, fmt.Errorf("empty object_key name")
	}
	switch {
	case strings.HasPrefix(name, prefixCanonical):
		return parseCanonical(name)

	case strings.HasPrefix(name, prefixTenant):
		tid, ok, err := splitTenantObjectKey(strings.TrimPrefix(name, prefixTenant))
		if err != nil {
			return CanonicalRef{}, fmt.Errorf("invalid object_key name %q: %w", name, err)
		}
		return CanonicalRef{TenantID: tid, ObjectKey: ok, Shape: ShapeTenant}, nil

	default:
		tid, err := auth.TenantFromContext(ctx)
		if err != nil {
			return CanonicalRef{}, fmt.Errorf("bare object_key %q requires a caller tenant: %w", name, err)
		}
		return CanonicalRef{TenantID: tid, ObjectKey: name, Shape: ShapeBare}, nil
	}
}

// ResolveObjectKeyNameWithBinding is ResolveObjectKeyName plus bare-shape (B)
// enrichment: a bare name carries no (backend, bucket), so it is completed to
// canonical using the caller-tenant's default binding (ADR-0010 Phase 3). The
// A and C shapes are returned unchanged — the lookup is consulted only for B,
// and only when the binding isn't already populated. A tenant with no default
// binding gets ErrNoDefaultBinding.
func ResolveObjectKeyNameWithBinding(ctx context.Context, name string, bindings DefaultBindingLookup) (CanonicalRef, error) {
	ref, err := ResolveObjectKeyName(ctx, name)
	if err != nil {
		return CanonicalRef{}, err
	}
	if ref.Shape != ShapeBare || (ref.BackendID != "" && ref.BucketName != "") {
		return ref, nil
	}
	backend, bucket, found, err := bindings.TenantDefaultBinding(ctx, ref.TenantID)
	if err != nil {
		return CanonicalRef{}, fmt.Errorf("resolve default binding for %s: %w", ref.TenantID, err)
	}
	if !found {
		return CanonicalRef{}, ErrNoDefaultBinding
	}
	ref.BackendID = backend
	ref.BucketName = bucket
	return ref, nil
}

// ResolveTenantParent parses the `parent` field shape "tenants/{t}" into a
// tenant id. An empty parent returns uuid.Nil (the "list across the caller's
// scope" convention the admin handlers use), preserving the prior
// tenantUUIDFromParent behaviour.
func ResolveTenantParent(parent string) (uuid.UUID, error) {
	if parent == "" {
		return uuid.Nil, nil
	}
	s := strings.TrimPrefix(parent, prefixTenant)
	// Tolerate a trailing "/..." (e.g. "tenants/{t}/objectKeys") by taking the
	// first segment as the id.
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid tenant parent %q: %w", parent, err)
	}
	return id, nil
}

// parseCanonical decodes storageBackends/{b}/buckets/{bk}/tenants/{t}/objectKeys/{ok}.
func parseCanonical(name string) (CanonicalRef, error) {
	bad := func() (CanonicalRef, error) {
		return CanonicalRef{}, fmt.Errorf("invalid canonical object_key name %q", name)
	}
	rest := strings.TrimPrefix(name, prefixCanonical)
	backend, rest, ok := strings.Cut(rest, "/buckets/")
	if !ok || backend == "" {
		return bad()
	}
	bucket, rest, ok := strings.Cut(rest, "/tenants/")
	if !ok || bucket == "" {
		return bad()
	}
	tid, okBody, err := splitTenantObjectKey(rest)
	if err != nil {
		return bad()
	}
	return CanonicalRef{
		TenantID:   tid,
		ObjectKey:  okBody,
		BackendID:  backend,
		BucketName: bucket,
		Shape:      ShapeCanonical,
	}, nil
}

// splitTenantObjectKey parses "{tid}/objectKeys/{ok}" → (tid, ok). ok may
// contain '/'; we anchor on the literal /objectKeys/ separator.
func splitTenantObjectKey(s string) (uuid.UUID, string, error) {
	i := strings.Index(s, objectKeysSep)
	if i <= 0 {
		return uuid.Nil, "", fmt.Errorf("missing %q in %q", objectKeysSep, s)
	}
	tid, err := uuid.Parse(s[:i])
	if err != nil {
		return uuid.Nil, "", err
	}
	ok := s[i+len(objectKeysSep):]
	if ok == "" {
		return uuid.Nil, "", fmt.Errorf("empty object_key body in %q", s)
	}
	return tid, ok, nil
}
