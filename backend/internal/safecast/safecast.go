package safecast

import "math"

// Int32 safely casts an int to int32, capping at math.MaxInt32.
func Int32(v int) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}

	return int32(v)
}

// Int32WithFallback safely casts an int to int32 with a specific fallback value
// if the input exceeds math.MaxInt32.
func Int32WithFallback(v int, fallback int32) int32 {
	if v > math.MaxInt32 {
		return fallback
	}
	if v < math.MinInt32 {
		return int32(math.MinInt32)
	}

	return int32(v)
}

// Int64 safely casts a uint64 to int64, capping at math.MaxInt64.
func Int64(v uint64) int64 {
	if v > uint64(math.MaxInt64) {
		return math.MaxInt64
	}

	return int64(v)
}

// Uint32 safely casts an int to uint32, capping at math.MaxUint32 and 0.
func Uint32(v int) uint32 {
	if v < 0 {
		return 0
	}
	if uint64(v) > math.MaxUint32 {
		return math.MaxUint32
	}

	return uint32(v)
}

// IntFrom32 safely casts an int32 to int.
func IntFrom32(v int32) int {
	return int(v)
}

// IntFrom64 safely casts an int64 to int, capping at math.MaxInt.
func IntFrom64(v int64) int {
	if v > int64(math.MaxInt) {
		return math.MaxInt
	}
	if v < int64(math.MinInt) {
		return math.MinInt
	}

	return int(v)
}

// Int32From64 safely casts an int64 to int32, capping at math.MaxInt32.
func Int32From64(v int64) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}

	return int32(v)
}

// Uint32From64 safely casts a uint64 to uint32, capping at math.MaxUint32.
func Uint32From64(v uint64) uint32 {
	if v > math.MaxUint32 {
		return math.MaxUint32
	}

	return uint32(v)
}

// UintFrom64 safely casts a uint64 to uint, capping at math.MaxUint.
func UintFrom64(v uint64) uint {
	if v > uint64(math.MaxUint) {
		return math.MaxUint
	}

	return uint(v)
}
