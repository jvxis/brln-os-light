package reports

import "testing"

// The comparison direction decides whether a recalculation preserves history or
// destroys it, and inverting it would be invisible: both directions produce a
// plausible-looking report.
func TestCompareStoredComponent(t *testing.T) {
	cases := []struct {
		name               string
		stored, recomputed int64
		want               DryRunVerdict
		safe               bool
	}{
		{"identical", 1200, 1200, DryRunMatches, true},
		{"node no longer has it", 1200, 900, DryRunPruned, false},
		{"pruned to nothing", 1200, 0, DryRunPruned, false},
		{"original run saw less", 1200, 1500, DryRunHigher, true},
		{"both empty", 0, 0, DryRunMatches, true},
		{"first data for the day", 0, 500, DryRunHigher, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CompareStoredComponent(tc.stored, tc.recomputed)
			if got != tc.want {
				t.Fatalf("stored %d, recomputed %d: got %v, want %v",
					tc.stored, tc.recomputed, got, tc.want)
			}
			if got.SafeToRecalculate() != tc.safe {
				t.Fatalf("%v: safe=%t, want %t", got, got.SafeToRecalculate(), tc.safe)
			}
		})
	}
}
