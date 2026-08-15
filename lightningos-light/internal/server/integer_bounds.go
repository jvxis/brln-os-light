package server

import "math"

func uint64ToInt64(value uint64) (int64, bool) {
	if value > math.MaxInt64 {
		return 0, false
	}
	return int64(value), true
}

func uint32ToInt32(value uint32) (int32, bool) {
	if value > math.MaxInt32 {
		return 0, false
	}
	return int32(value), true
}

func intToInt32(value int) (int32, bool) {
	if value < math.MinInt32 || value > math.MaxInt32 {
		return 0, false
	}
	return int32(value), true
}

func intToInt32Or(value int, fallback int32) int32 {
	converted, ok := intToInt32(value)
	if !ok {
		return fallback
	}
	return converted
}
