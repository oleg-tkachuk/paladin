package apiutil

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ParseCollectionName parses "collections/{collection}".
func ParseCollectionName(name string) (string, error) {
	const prefix = "collections/"
	if !strings.HasPrefix(name, prefix) || len(name) <= len(prefix) {
		return "", fmt.Errorf("invalid collection name %q", name)
	}
	return name[len(prefix):], nil
}

// TenantRef is the parsed form of a tenant resource name. Exactly one of
// {ID, Slug} is set:
//
//   - ID:   the caller passed `tenants/{uuid}` or a bare UUID.
//   - Slug: the caller passed `tenants/{slug}` where {slug} matches the
//     kebab-case slug format. Handlers that accept slugs must resolve
//     Slug → uuid via the tenant repository before issuing further queries.
type TenantRef struct {
	ID   uuid.UUID
	Slug string
}

// HasID reports whether the ref carries a UUID directly (no slug lookup needed).
func (r TenantRef) HasID() bool { return r.ID != uuid.Nil }

// TenantNamePrefix starts every tenant resource name: "tenants/{tenant_id_or_slug}".
const TenantNamePrefix = "tenants/"

// ParseTenantName parses "tenants/{tenant_id}". Bare UUIDs are accepted as a
// permissive fallback — matches the pre-existing behavior callers rely on.
//
// This UUID-only form is preserved for backwards compatibility with the many
// handlers that still hard-require a UUID. New handlers that want to accept
// slugs should use ParseTenantNameRef instead.
func ParseTenantName(name string) (uuid.UUID, error) {
	const prefix = TenantNamePrefix
	if strings.HasPrefix(name, prefix) {
		return uuid.Parse(name[len(prefix):])
	}
	return uuid.Parse(name)
}

// ParseTenantNameRef parses "tenants/{tenant_id_or_slug}" and returns a
// TenantRef carrying either the UUID or the slug. Handlers that wish to
// accept the human-readable slug form call this and resolve the slug via
// the tenant repository.
func ParseTenantNameRef(name string) (TenantRef, error) {
	const prefix = TenantNamePrefix
	body := name
	if strings.HasPrefix(name, prefix) {
		body = name[len(prefix):]
	}
	if body == "" {
		return TenantRef{}, fmt.Errorf("invalid tenant name %q", name)
	}
	if id, err := uuid.Parse(body); err == nil {
		return TenantRef{ID: id}, nil
	}
	if IsTenantSlug(body) {
		return TenantRef{Slug: body}, nil
	}
	return TenantRef{}, fmt.Errorf("invalid tenant name %q (want UUID or kebab-case slug)", name)
}

// ParseOperationName parses "operations/{operation_id}".
func ParseOperationName(name string) (uuid.UUID, error) {
	const prefix = "operations/"
	if strings.HasPrefix(name, prefix) {
		return uuid.Parse(name[len(prefix):])
	}
	return uuid.Parse(name)
}
