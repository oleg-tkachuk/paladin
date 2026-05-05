package apiutil

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ParseObjectName parses "object_keys/{object_key}/objects/{object_id}".
//
// AIP-122: object names are slash-separated collection/id pairs. Empty
// {object_key} segments are rejected — a path like "object_keys//objects/..."
// has no useful meaning and previously slipped through one of the three
// in-tree copies of this parser.
func ParseObjectName(name string) (objectKey string, objectID uuid.UUID, err error) {
	const (
		bucketsPrefix = "object_keys/"
		objectsPrefix = "objects/"
	)
	if !strings.HasPrefix(name, bucketsPrefix) {
		return "", uuid.Nil, fmt.Errorf("invalid object name %q", name)
	}
	rest := name[len(bucketsPrefix):]
	sep := strings.IndexByte(rest, '/')
	if sep <= 0 {
		return "", uuid.Nil, fmt.Errorf("invalid object name %q", name)
	}
	objectKey = rest[:sep]
	remainder := rest[sep+1:]
	if !strings.HasPrefix(remainder, objectsPrefix) {
		return "", uuid.Nil, fmt.Errorf("invalid object name %q", name)
	}
	idStr := remainder[len(objectsPrefix):]
	if idStr == "" {
		return "", uuid.Nil, fmt.Errorf("invalid object name %q", name)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return "", uuid.Nil, fmt.Errorf("invalid object_id in %q: %w", name, err)
	}
	return objectKey, id, nil
}

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
