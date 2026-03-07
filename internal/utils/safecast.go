package utils

import "math"

// ToInt32Safe safely casts an int to int32, capping at math.MaxInt32.
// It is used to prevent G115 integer overflow findings.
func ToInt32Safe(v int) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return int32(v)
}

// ToInt32WithFallback safely casts an int to int32 with a specific fallback value
// if the input exceeds math.MaxInt32.
func ToInt32WithFallback(v int, fallback int32) int32 {
	if v > math.MaxInt32 {
		return fallback
	}
	if v < math.MinInt32 {
		return int32(math.MinInt32)
	}
	return int32(v)
}

// ToInt64Safe safely casts a uint64 to int64, capping at math.MaxInt64.
func ToInt64Safe(v uint64) int64 {
	if v > uint64(math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(v)
}

// ToUint32Safe safely casts an int to uint32, capping at math.MaxUint32 and 0.
func ToUint32Safe(v int) uint32 {
	if v < 0 {
		return 0
	}
	if uint64(v) > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v)
}
