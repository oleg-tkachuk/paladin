// Package resolve centralises object_key / tenant resource-name parsing for
// the connectshim edge. Phase 2 of backend/docs/canonical-resource-names.md:
// clients may send any of three name shapes, and every handler that needs the
// (tenant, object_key) tuple goes through one resolver instead of each
// re-implementing the anchor-on-prefix parse.
package resolve

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/oleg-tkachuk/paladin/internal/auth"
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
// are populated only when the canonical (A) shape carried them.
type CanonicalRef struct {
	TenantID   uuid.UUID
	ObjectKey  string
	BackendID  string
	BucketName string
	Shape      Shape
}

// shapeTotal lets us see the real-world distribution of the three name shapes
// before deciding whether to deprecate any of them (Phase 3).
var shapeTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "paladin_resource_name_shape_total",
	Help: "ObjectKey resource-name shapes received at the connectshim edge, by shape.",
}, []string{"shape"})

func init() { prometheus.MustRegister(shapeTotal) }

const (
	prefixCanonical = "storageBackends/"
	prefixTenant    = "tenants/"
	objectKeysSep   = "/objectKeys/"
)

// ResolveObjectKeyName parses any of the three object_key name shapes into a
// CanonicalRef. `ok` may contain '/'. The bare (B) shape carries no tenant in
// the name, so the caller's tenant is read from ctx. The shape is recorded.
func ResolveObjectKeyName(ctx context.Context, name string) (CanonicalRef, error) {
	if name == "" {
		return CanonicalRef{}, fmt.Errorf("empty object_key name")
	}
	switch {
	case strings.HasPrefix(name, prefixCanonical):
		ref, err := parseCanonical(name)
		if err != nil {
			return CanonicalRef{}, err
		}
		shapeTotal.WithLabelValues(string(ShapeCanonical)).Inc()
		return ref, nil

	case strings.HasPrefix(name, prefixTenant):
		tid, ok, err := splitTenantObjectKey(strings.TrimPrefix(name, prefixTenant))
		if err != nil {
			return CanonicalRef{}, fmt.Errorf("invalid object_key name %q: %w", name, err)
		}
		shapeTotal.WithLabelValues(string(ShapeTenant)).Inc()
		return CanonicalRef{TenantID: tid, ObjectKey: ok, Shape: ShapeTenant}, nil

	default:
		tid, err := auth.TenantFromContext(ctx)
		if err != nil {
			return CanonicalRef{}, fmt.Errorf("bare object_key %q requires a caller tenant: %w", name, err)
		}
		shapeTotal.WithLabelValues(string(ShapeBare)).Inc()
		return CanonicalRef{TenantID: tid, ObjectKey: name, Shape: ShapeBare}, nil
	}
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
