package utils

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/go-playground/validator/v10"
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

var (
	pathSegmentRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	contentTypeRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9!#$&\-^_.+]{0,126}/[a-zA-Z0-9][a-zA-Z0-9!#$&\-^_.+]{0,126}(;.*)?$`)
	labelKeyRegex    = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

	v *validator.Validate
)

func init() {
	v = validator.New()
	_ = v.RegisterValidation("path_segment", func(fl validator.FieldLevel) bool {
		s := fl.Field().String()
		if s == ".." || s == "." {
			return false
		}

		return pathSegmentRegex.MatchString(s)
	})
	_ = v.RegisterValidation("content_type", func(fl validator.FieldLevel) bool {
		return contentTypeRegex.MatchString(fl.Field().String())
	})
	_ = v.RegisterValidation("label_key", func(fl validator.FieldLevel) bool {
		return labelKeyRegex.MatchString(fl.Field().String())
	})
}

// ValidatePathSegment checks that a single component of an S3 key path is valid.
func ValidatePathSegment(segment string) error {
	if err := v.Var(segment, "required,printascii,path_segment"); err != nil {
		return fmt.Errorf("invalid path segment %q", segment)
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
func ValidateSubpath(subpath string) error {
	if subpath == "" {
		return nil
	}
	if err := v.Var(subpath, fmt.Sprintf("max=%d", MaxSubpathLength)); err != nil {
		return fmt.Errorf("subpath too long")
	}
	if strings.HasPrefix(subpath, "/") || strings.HasSuffix(subpath, "/") || strings.Contains(subpath, "//") {
		return fmt.Errorf("subpath must not start or end with '/' or contain consecutive slashes")
	}
	for _, seg := range strings.Split(subpath, "/") {
		if err := ValidatePathSegment(seg); err != nil {
			return fmt.Errorf("invalid subpath segment: %w", err)
		}
	}

	return nil
}

// ValidateKeyPrefix validates a client-supplied key_prefix filter.
func ValidateKeyPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if err := v.Var(prefix, fmt.Sprintf("max=%d", MaxKeyPrefixLength)); err != nil {
		return fmt.Errorf("key_prefix too long")
	}
	if strings.HasPrefix(prefix, "/") || strings.Contains(prefix, "..") || strings.Contains(prefix, "//") || strings.Contains(prefix, "%") {
		return fmt.Errorf("invalid key_prefix format")
	}

	return nil
}

// ValidateContentType checks if content type format is valid
func ValidateContentType(contentType string) error {
	if err := v.Var(contentType, "required,max=255,content_type"); err != nil {
		return fmt.Errorf("invalid content type format")
	}

	return nil
}

// ValidateExternalRef checks for path traversal and SQL injection attempts
func ValidateExternalRef(externalRef string) error {
	if externalRef == "" {
		return nil
	}
	if err := v.Var(externalRef, fmt.Sprintf("max=%d,printascii", MaxExternalRefLength)); err != nil {
		return fmt.Errorf("invalid external_ref")
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
		if err := v.Var(key, fmt.Sprintf("required,max=%d,label_key", MaxLabelKeyLength)); err != nil {
			return fmt.Errorf("invalid label key %q", key)
		}
		if err := v.Var(value, fmt.Sprintf("max=%d,printascii", MaxLabelValueLength)); err != nil {
			return fmt.Errorf("invalid label value for key %q", key)
		}
	}

	return nil
}

// ValidateUUID checks if a string is a valid UUID v4
func ValidateUUID(id string) error {
	if err := v.Var(strings.ToLower(id), "required,uuid4"); err != nil {
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
