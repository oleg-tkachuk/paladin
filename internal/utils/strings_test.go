package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseSizeString(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		wantErr  bool
	}{
		{"1KiB", 1024, false},
		{"1MiB", 1024 * 1024, false},
		{"1GiB", 1024 * 1024 * 1024, false},
		{"1000B", 1000, false},
		{"invalid", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseSizeString(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, got)
			}
		})
	}
}
