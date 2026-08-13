package server

import (
	"math"
	"strconv"
	"testing"
)

func TestIntegerBoundsRejectOverflow(t *testing.T) {
	if got, ok := uint64ToInt64(math.MaxInt64); !ok || got != math.MaxInt64 {
		t.Fatalf("max int64 conversion failed: got=%d ok=%t", got, ok)
	}
	if _, ok := uint64ToInt64(uint64(math.MaxInt64) + 1); ok {
		t.Fatal("uint64 above max int64 was accepted")
	}
	if got, ok := uint32ToInt32(math.MaxInt32); !ok || got != math.MaxInt32 {
		t.Fatalf("max int32 conversion failed: got=%d ok=%t", got, ok)
	}
	if _, ok := uint32ToInt32(uint32(math.MaxInt32) + 1); ok {
		t.Fatal("uint32 above max int32 was accepted")
	}
}

func TestParseInt32AnyRejectsOverflowAndFractions(t *testing.T) {
	const fallback int32 = 3
	for _, value := range []any{
		strconv.FormatInt(math.MaxInt32+1, 10),
		strconv.FormatInt(math.MinInt32-1, 10),
		float64(math.MaxInt32) + 1,
		1.5,
	} {
		if got := parseInt32Any(value, fallback); got != fallback {
			t.Fatalf("overflow/fraction %v produced %d, want fallback %d", value, got, fallback)
		}
	}
	if got := parseInt32Any("42", fallback); got != 42 {
		t.Fatalf("valid int32 parsed as %d", got)
	}
}
