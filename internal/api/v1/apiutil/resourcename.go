package apiutil

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ParseObjectKeyName parses "object_keys/{object_key}".
func ParseObjectKeyName(name string) (string, error) {
	const prefix = "object_keys/"
	if !strings.HasPrefix(name, prefix) || len(name) <= len(prefix) {
		return "", fmt.Errorf("invalid object_key name %q", name)
	}
	return name[len(prefix):], nil
}

// ParseTenantName parses "tenants/{tenant_id}". Bare UUIDs are accepted as a
// permissive fallback — matches the pre-existing behavior callers rely on.
func ParseTenantName(name string) (uuid.UUID, error) {
	const prefix = "tenants/"
	if strings.HasPrefix(name, prefix) {
		return uuid.Parse(name[len(prefix):])
	}
	return uuid.Parse(name)
}

// ParseOperationName parses "operations/{operation_id}".
func ParseOperationName(name string) (uuid.UUID, error) {
	const prefix = "operations/"
	if strings.HasPrefix(name, prefix) {
		return uuid.Parse(name[len(prefix):])
	}
	return uuid.Parse(name)
}
