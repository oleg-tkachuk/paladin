package safecast

import (
	"math"
	"testing"
)

func TestInt32(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected int32
	}{
		{"within range", 100, 100},
		{"max int32", math.MaxInt32, math.MaxInt32},
		{"min int32", math.MinInt32, math.MinInt32},
		{"above max int32", math.MaxInt32 + 1, math.MaxInt32},
		{"below min int32", math.MinInt32 - 1, math.MinInt32},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Int32(tt.input); got != tt.expected {
				t.Errorf("Int32() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestInt32WithFallback(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		fallback int32
		expected int32
	}{
		{"within range", 100, -1, 100},
		{"above max", math.MaxInt32 + 1, -1, -1},
		{"below min", math.MinInt32 - 1, -1, math.MinInt32},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Int32WithFallback(tt.input, tt.fallback); got != tt.expected {
				t.Errorf("Int32WithFallback() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestInt64(t *testing.T) {
	tests := []struct {
		name     string
		input    uint64
		expected int64
	}{
		{"within range", 100, 100},
		{"max int64", math.MaxInt64, math.MaxInt64},
		{"above max int64", uint64(math.MaxInt64) + 1, math.MaxInt64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Int64(tt.input); got != tt.expected {
				t.Errorf("Int64() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestUint32(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected uint32
	}{
		{"within range", 100, 100},
		{"zero", 0, 0},
		{"negative", -1, 0},
		{"max uint32", math.MaxUint32, math.MaxUint32},
		{"above max uint32", math.MaxUint32 + 1, math.MaxUint32},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Uint32(tt.input); got != tt.expected {
				t.Errorf("Uint32() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIntFrom32(t *testing.T) {
	input := int32(100)
	expected := 100
	if got := IntFrom32(input); got != expected {
		t.Errorf("IntFrom32() = %v, want %v", got, expected)
	}
}

func TestIntFrom64(t *testing.T) {
	tests := []struct {
		name     string
		input    int64
		expected int
	}{
		{"within range", 100, 100},
		{"max int", int64(math.MaxInt), math.MaxInt},
		{"min int", int64(math.MinInt), math.MinInt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IntFrom64(tt.input); got != tt.expected {
				t.Errorf("IntFrom64() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestInt32From64(t *testing.T) {
	tests := []struct {
		name     string
		input    int64
		expected int32
	}{
		{"within range", 100, 100},
		{"max int32", math.MaxInt32, math.MaxInt32},
		{"min int32", math.MinInt32, math.MinInt32},
		{"above max int32", int64(math.MaxInt32) + 1, math.MaxInt32},
		{"below min int32", int64(math.MinInt32) - 1, math.MinInt32},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Int32From64(tt.input); got != tt.expected {
				t.Errorf("Int32From64() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestUint32From64(t *testing.T) {
	tests := []struct {
		name     string
		input    uint64
		expected uint32
	}{
		{"within range", 100, 100},
		{"max uint32", math.MaxUint32, math.MaxUint32},
		{"above max uint32", uint64(math.MaxUint32) + 1, math.MaxUint32},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Uint32From64(tt.input); got != tt.expected {
				t.Errorf("Uint32From64() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestUintFrom64(t *testing.T) {
	tests := []struct {
		name     string
		input    uint64
		expected uint
	}{
		{"within range", 100, 100},
		{"max uint", uint64(math.MaxUint), math.MaxUint},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UintFrom64(tt.input); got != tt.expected {
				t.Errorf("UintFrom64() = %v, want %v", got, tt.expected)
			}
		})
	}
}
