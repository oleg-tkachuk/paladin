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
)

var (
	// Valid content type pattern (type/subtype with optional parameters)
	contentTypeRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9!#$&\-^_.+]{0,126}/[a-zA-Z0-9][a-zA-Z0-9!#$&\-^_.+]{0,126}(;.*)?$`)

	// Valid label key pattern (alphanumeric, dash, underscore, dot)
	labelKeyRegex = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

	// UUID v4 pattern
	uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

	// Dangerous patterns for path traversal and SQL injection
	pathTraversalPatterns = []string{"../", "..\\", "%2e%2e", "%252e%252e"}
	sqlInjectionPatterns  = []string{"'", "\"", ";", "--", "/*", "*/", "xp_", "sp_", "DROP", "SELECT", "INSERT", "UPDATE", "DELETE"}
)

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
		return nil // Optional field
	}

	if len(externalRef) > MaxExternalRefLength {
		return fmt.Errorf("external_ref too long (max %d characters)", MaxExternalRefLength)
	}

	if !utf8.ValidString(externalRef) {
		return fmt.Errorf("external_ref contains invalid UTF-8")
	}

	// Check for path traversal attempts
	lowerRef := strings.ToLower(externalRef)
	for _, pattern := range pathTraversalPatterns {
		if strings.Contains(lowerRef, pattern) {
			return fmt.Errorf("external_ref contains invalid characters")
		}
	}

	// Check for SQL injection patterns (basic check)
	for _, pattern := range sqlInjectionPatterns {
		if strings.Contains(strings.ToUpper(externalRef), strings.ToUpper(pattern)) {
			return fmt.Errorf("external_ref contains invalid characters")
		}
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
		// Validate key
		if key == "" {
			return fmt.Errorf("label key cannot be empty")
		}

		if len(key) > MaxLabelKeyLength {
			return fmt.Errorf("label key too long (max %d characters)", MaxLabelKeyLength)
		}

		if !labelKeyRegex.MatchString(key) {
			return fmt.Errorf("label key '%s' contains invalid characters (only alphanumeric, dash, underscore, dot allowed)", key)
		}

		// Validate value
		if len(value) > MaxLabelValueLength {
			return fmt.Errorf("label value for key '%s' too long (max %d characters)", key, MaxLabelValueLength)
		}

		if !utf8.ValidString(value) {
			return fmt.Errorf("label value for key '%s' contains invalid UTF-8", key)
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
