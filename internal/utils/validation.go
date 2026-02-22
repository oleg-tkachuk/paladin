package utils

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Validation constants
const (
	MaxLabelKeyLength    = 128
	MaxLabelValueLength  = 1024
	MaxExternalRefLength = 256
	MaxLabelsCount       = 50
	MaxSubpathLength     = 256
	MaxKeyPrefixLength   = 256
)

// Strict segment regex for S3 key path segments:
// Allowed characters: a-z, 0-9, '-', '_'
// Must start with a-z or 0-9, max 64 chars total.
var (
	// pathSegmentRegex validates a single path segment (tenant, category, or any subpath component)
	pathSegmentRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

	// Valid content type pattern (type/subtype with optional parameters)
	contentTypeRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9!#$&\-^_.+]{0,126}/[a-zA-Z0-9][a-zA-Z0-9!#$&\-^_.+]{0,126}(;.*)?$`)

	// Valid label key pattern (alphanumeric, dash, underscore, dot)
	labelKeyRegex = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

	// UUID v4 pattern
	uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

// ValidatePathSegment checks that a single component of an S3 key path is valid.
//
// Rules (applied to tenant, category, and each subpath segment individually):
//   - ASCII lowercase letters, digits, '-', '_' only
//   - Must start with [a-z0-9]
//   - 1–64 chars total
//   - Must not be ".." or "."
//   - Must not contain control characters or non-ASCII
func ValidatePathSegment(segment string) error {
	if segment == "" {
		return fmt.Errorf("path segment cannot be empty")
	}
	if segment == ".." || segment == "." {
		return fmt.Errorf("path segment %q is not allowed", segment)
	}
	if !utf8.ValidString(segment) {
		return fmt.Errorf("path segment contains invalid UTF-8")
	}
	for _, r := range segment {
		if r > 127 {
			return fmt.Errorf("path segment must not contain non-ASCII characters")
		}
		if r < 32 {
			return fmt.Errorf("path segment must not contain control characters")
		}
	}
	if !pathSegmentRegex.MatchString(segment) {
		return fmt.Errorf("invalid path segment %q: must match ^[a-z0-9][a-z0-9_-]{0,63}$", segment)
	}
	return nil
}

// ValidateCategorySlug checks a category slug against the strict path segment rules.
func ValidateCategorySlug(slug string) error {
	if err := ValidatePathSegment(slug); err != nil {
		return fmt.Errorf("invalid category slug: %w", err)
	}
	return nil
}

// ValidateSubpath checks an optional subpath within a category.
// The subpath may contain '/' to separate its own segments; each segment is validated
// individually against the strict path segment rules.
//
// Rules:
//   - Empty subpath is allowed (means no subpath)
//   - Must not start or end with '/'
//   - Must not contain '//' (empty segments)
//   - Each segment must pass ValidatePathSegment
//   - Total length ≤ 256
func ValidateSubpath(subpath string) error {
	if subpath == "" {
		return nil
	}
	if len(subpath) > MaxSubpathLength {
		return fmt.Errorf("subpath too long (max %d characters)", MaxSubpathLength)
	}
	if strings.HasPrefix(subpath, "/") || strings.HasSuffix(subpath, "/") {
		return fmt.Errorf("subpath must not start or end with '/'")
	}
	if strings.Contains(subpath, "//") {
		return fmt.Errorf("subpath must not contain consecutive slashes")
	}
	for _, seg := range strings.Split(subpath, "/") {
		if err := ValidatePathSegment(seg); err != nil {
			return fmt.Errorf("invalid subpath segment: %w", err)
		}
	}
	return nil
}

// ValidateKeyPrefix validates a client-supplied key_prefix filter.
// The prefix is relative to tenant_id/category/ and must not escape that scope.
//
// Rules:
//   - Empty prefix means "no filter" (allowed)
//   - Must not start or end with '/'
//   - Must not contain '..' or '//'
//   - Must not contain SQL wildcard characters ('%', '_' are legitimate in slugs, but '%' is not)
//   - Each segment must satisfy pathSegmentRegex
//   - Total length ≤ 256
func ValidateKeyPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if len(prefix) > MaxKeyPrefixLength {
		return fmt.Errorf("key_prefix too long (max %d characters)", MaxKeyPrefixLength)
	}
	if strings.HasPrefix(prefix, "/") {
		return fmt.Errorf("key_prefix must not start with '/'")
	}
	if strings.Contains(prefix, "..") {
		return fmt.Errorf("key_prefix must not contain '..'")
	}
	if strings.Contains(prefix, "//") {
		return fmt.Errorf("key_prefix must not contain consecutive slashes")
	}
	// Reject SQL LIKE wildcard '%' (underscore is allowed in path segments but not as a wildcard)
	if strings.Contains(prefix, "%") {
		return fmt.Errorf("key_prefix must not contain '%%'")
	}
	return nil
}

// ValidateContentType checks if content type format is valid
func ValidateContentType(contentType string) error {
	if contentType == "" {
		return fmt.Errorf("content type cannot be empty")
	}
	if len(contentType) > 255 {
		return fmt.Errorf("content type too long (max 255 characters)")
	}
	if !contentTypeRegex.MatchString(contentType) {
		return fmt.Errorf("invalid content type format")
	}
	return nil
}

// ValidateExternalRef checks for path traversal and SQL injection attempts
func ValidateExternalRef(externalRef string) error {
	if externalRef == "" {
		return nil
	}
	if len(externalRef) > MaxExternalRefLength {
		return fmt.Errorf("external_ref too long (max %d characters)", MaxExternalRefLength)
	}
	if !utf8.ValidString(externalRef) {
		return fmt.Errorf("external_ref contains invalid UTF-8")
	}
	return nil
}

// ValidateLabels checks label keys and values for safety
func ValidateLabels(labels map[string]string) error {
	if labels == nil {
		return nil
	}
	if len(labels) > MaxLabelsCount {
		return fmt.Errorf("too many labels (max %d)", MaxLabelsCount)
	}
	for key, value := range labels {
		if key == "" {
			return fmt.Errorf("label key cannot be empty")
		}
		if len(key) > MaxLabelKeyLength {
			return fmt.Errorf("label key too long (max %d characters)", MaxLabelKeyLength)
		}
		if !labelKeyRegex.MatchString(key) {
			return fmt.Errorf("label key %q contains invalid characters (only alphanumeric, dash, underscore, dot allowed)", key)
		}
		if len(value) > MaxLabelValueLength {
			return fmt.Errorf("label value for key %q too long (max %d characters)", key, MaxLabelValueLength)
		}
		if !utf8.ValidString(value) {
			return fmt.Errorf("label value for key %q contains invalid UTF-8", key)
		}
	}
	return nil
}

// ValidateUUID checks if a string is a valid UUID v4
func ValidateUUID(id string) error {
	if id == "" {
		return fmt.Errorf("UUID cannot be empty")
	}
	if !uuidRegex.MatchString(strings.ToLower(id)) {
		return fmt.Errorf("invalid UUID format")
	}
	return nil
}

// ValidateSizeBytes checks if size is within reasonable bounds
func ValidateSizeBytes(sizeBytes int64, maxSize int64) error {
	if sizeBytes <= 0 {
		return fmt.Errorf("size_bytes must be positive")
	}
	if sizeBytes > maxSize {
		return fmt.Errorf("size_bytes exceeds maximum allowed size (%d bytes)", maxSize)
	}
	return nil
}
