package validation

import (
	"strings"
	"testing"
)

func TestPathSegment(t *testing.T) {
	tests := []struct {
		name    string
		segment string
		wantErr bool
	}{
		{"valid", "my-segment", false},
		{"valid with numbers", "segment123", false},
		{"valid with spaces", "my segment", false},
		{"valid starts with hyphen", "-segment", false},
		{"valid starts with dot", ".segment", false},
		{"invalid dots", "..", true},
		{"invalid dot", ".", true},
		{"too long", strings.Repeat("a", 1025), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := PathSegment(tt.segment); (err != nil) != tt.wantErr {
				t.Errorf("PathSegment() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCategorySlug(t *testing.T) {
	tests := []struct {
		name    string
		slug    string
		wantErr bool
	}{
		{"valid", "my/category", false},
		{"valid complex", "System (Logs).v1/2024+Production Data", false},
		{"valid single", "category", false},
		{"invalid empty", "", true},
		{"invalid segment", "my/../cat", true},
		{"too long", strings.Repeat("a", 1000), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := CategorySlug(tt.slug); (err != nil) != tt.wantErr {
				t.Errorf("CategorySlug() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestContentType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		wantErr     bool
	}{
		{"valid", "application/json", false},
		{"valid with charset", "text/plain; charset=utf-8", false},
		{"invalid format", "invalid-type", true},
		{"empty", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ContentType(tt.contentType); (err != nil) != tt.wantErr {
				t.Errorf("ContentType() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSizeBytes(t *testing.T) {
	maxSize := int64(1024 * 1024)
	tests := []struct {
		name    string
		size    int64
		wantErr bool
	}{
		{"valid", 500, false},
		{"at max", maxSize, false},
		{"over max", maxSize + 1, true},
		{"zero", 0, true},
		{"negative", -1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := SizeBytes(tt.size, maxSize); (err != nil) != tt.wantErr {
				t.Errorf("SizeBytes() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestUUID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"valid", "550e8400-e29b-41d4-a716-446655440000", false},
		{"invalid", "not-a-uuid", true},
		{"empty", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := UUID(tt.id); (err != nil) != tt.wantErr {
				t.Errorf("UUID() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLabels(t *testing.T) {
	tests := []struct {
		name    string
		labels  map[string]string
		wantErr bool
	}{
		{"valid", map[string]string{"key": "value"}, false},
		{"empty", map[string]string{}, false},
		{"nil", nil, false},
		{"invalid key", map[string]string{"key!": "value"}, true},
		{"too many", func() map[string]string {
			m := make(map[string]string)
			for i := 0; i < 51; i++ {
				m[strings.Repeat("a", i)] = "v"
			}

			return m
		}(), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Labels(tt.labels); (err != nil) != tt.wantErr {
				t.Errorf("Labels() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
