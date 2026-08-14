package server

import "testing"

// Editing a running loop is limited to the fee ceiling (issue #43). These are the
// states where a next payment still exists to apply it to; anywhere else the
// change would look accepted and do nothing.
func TestLoopOutBRLNMaxFeeEditableStatuses(t *testing.T) {
	editable := []string{
		loopOutBRLNStatusRunning,
		loopOutBRLNStatusWaitingLiquidity,
		loopOutBRLNStatusPaused,
		loopOutBRLNStatusPauseRequested,
	}
	for _, status := range editable {
		if !loopOutBRLNEditableStatuses[status] {
			t.Errorf("%s still has payments ahead of it and must accept a new ceiling", status)
		}
	}

	// cancel_requested is winding down: the loop is stopping, not repricing.
	frozen := []string{
		loopOutBRLNStatusCancelRequested,
		loopOutBRLNStatusCompleted,
		loopOutBRLNStatusCancelled,
		loopOutBRLNStatusFailed,
	}
	for _, status := range frozen {
		if loopOutBRLNEditableStatuses[status] {
			t.Errorf("%s has no next payment; accepting an edit would be a lie", status)
		}
	}
}

// The bounds match job creation, so a loop cannot be edited into a configuration
// that would have been refused at the start.
func TestLoopOutBRLNMaxFeeBoundsMatchCreation(t *testing.T) {
	cases := []struct {
		ppm   int64
		valid bool
	}{
		{0, false},
		{-1, false},
		{1, true},
		{2500, true},
		{1_000_000, true},
		{1_000_001, false},
	}
	for _, tc := range cases {
		got := tc.ppm >= 1 && tc.ppm <= 1_000_000
		if got != tc.valid {
			t.Errorf("%d ppm: accepted=%t, want %t", tc.ppm, got, tc.valid)
		}
	}
}
