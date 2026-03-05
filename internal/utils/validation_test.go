package utils

import (
	"strings"
	"testing"
)

func TestValidateContentType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		wantErr     bool
	}{
		{"valid application/json", "application/json", false},
		{"valid image/png", "image/png", false},
		{"valid with params", "text/html; charset=utf-8", false},
		{"empty", "", true},
		{"too long", strings.Repeat("a", 256), true},
		{"invalid format", "invalid", true},
		{"invalid chars", "application/json<script>", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateContentType(tt.contentType)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateContentType() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateExternalRef(t *testing.T) {
	tests := []struct {
		name        string
		externalRef string
		wantErr     bool
	}{
		{"valid ref", "user-123-doc-456", false},
		{"empty (optional)", "", false},
		{"too long", strings.Repeat("a", 257), true},
		{"invalid utf8", string([]byte{0xff, 0xfe, 0xfd}), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateExternalRef(tt.externalRef)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateExternalRef() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateLabels(t *testing.T) {
	tests := []struct {
		name    string
		labels  map[string]string
		wantErr bool
	}{
		{"nil labels", nil, false},
		{"empty labels", map[string]string{}, false},
		{"valid labels", map[string]string{"env": "prod", "version": "1.0"}, false},
		{"empty key", map[string]string{"": "value"}, true},
		{"key too long", map[string]string{strings.Repeat("a", 129): "value"}, true},
		{"invalid key chars", map[string]string{"key@invalid": "value"}, true},
		{"value too long", map[string]string{"key": strings.Repeat("a", 1025)}, true},
		{"too many labels", func() map[string]string {
			m := make(map[string]string)
			for i := 0; i < 51; i++ {
				m[string(rune('a'+i))] = "value"
			}

			return m
		}(), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateLabels(tt.labels)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateLabels() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateUUID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"valid uuid v4", "550e8400-e29b-41d4-a716-446655440000", false},
		{"valid uuid lowercase", "550e8400-e29b-41d4-a716-446655440000", false},
		{"valid uuid uppercase", "550E8400-E29B-41D4-A716-446655440000", false},
		{"empty", "", true},
		{"invalid format", "not-a-uuid", true},
		{"wrong version", "550e8400-e29b-31d4-a716-446655440000", true},
		{"missing dashes", "550e8400e29b41d4a716446655440000", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUUID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateUUID() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSizeBytes(t *testing.T) {
	tests := []struct {
		name      string
		sizeBytes int64
		maxSize   int64
		wantErr   bool
	}{
		{"valid size", 1024, 10000, false},
		{"max size", 10000, 10000, false},
		{"zero", 0, 10000, true},
		{"negative", -1, 10000, true},
		{"exceeds max", 10001, 10000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSizeBytes(tt.sizeBytes, tt.maxSize)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSizeBytes() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
