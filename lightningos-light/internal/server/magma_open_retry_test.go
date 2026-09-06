package server

import (
	"testing"
	"time"
)

// Order 8ea03637, 2026-09-06. The peer answered when the order was accepted at
// 15:01:44 and was gone by the open at 15:03:20 - 96 seconds. The open failed
// with "peer disconnected", the order landed in needs_attention, and nothing
// retried it: the funding guard refuses that state, so the manual button was
// closed too. It took a database edit 23 minutes later, with the buyer already
// paid and the clock running.
//
// Amboss allows about two days to open after payment. Giving up in 75 seconds
// was never the shape of the problem.
func TestMagmaKeepsTryingWhileAmbossStillAllowsIt(t *testing.T) {
	firstSeen := time.Date(2026, 9, 6, 18, 0, 39, 0, time.UTC)
	deadline := time.Date(2026, 9, 8, 19, 1, 35, 0, time.UTC) // the real one, from Amboss

	duringTheFailure := time.Date(2026, 9, 6, 18, 4, 35, 0, time.UTC)
	if !magmaShouldKeepTryingToOpen(&deadline, firstSeen, duringTheFailure) {
		t.Fatal("minutes in, with two days left, the order must go back in line")
	}
	nearlyOutOfTime := deadline.Add(-time.Minute)
	if !magmaShouldKeepTryingToOpen(&deadline, firstSeen, nearlyOutOfTime) {
		t.Fatal("a minute of window left is still a minute worth trying")
	}
	if magmaShouldKeepTryingToOpen(&deadline, firstSeen, deadline.Add(time.Second)) {
		t.Fatal("past the deadline there is nothing left to win")
	}
}

// A missing deadline means unknown, never expired. Amboss reports none once an
// order settles, and a fetch can simply fail - abandoning a paid order because a
// field was absent is the outcome this path exists to prevent.
func TestMagmaMissingDeadlineDoesNotMeanExpired(t *testing.T) {
	firstSeen := time.Date(2026, 9, 6, 18, 0, 0, 0, time.UTC)
	if !magmaShouldKeepTryingToOpen(nil, firstSeen, firstSeen.Add(6*time.Hour)) {
		t.Fatal("without a deadline the fallback window must still be honoured")
	}
	// The fallback has to be generous enough to survive a node that was off for
	// hours: an outage is exactly when a paid order most needs someone trying.
	if magmaOpenRetryFallback < 12*time.Hour {
		t.Fatalf("the fallback window is too short to outlast an outage: %s", magmaOpenRetryFallback)
	}
	if magmaShouldKeepTryingToOpen(nil, firstSeen, firstSeen.Add(magmaOpenRetryFallback+time.Minute)) {
		t.Fatal("the fallback still has to end somewhere")
	}
}
