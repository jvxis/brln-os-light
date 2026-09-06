package server

import (
	"testing"
	"time"
)

// After payment the operator is the last line of defence - they can open by
// hand, reach the buyer, or talk to Amboss. Until now nothing told them
// anything, and making the retry patient had quietly made it silent too: the
// deferral is an info event that never leaves the database, replacing a warning
// that used to fire on the first failed open.
//
// The first alert is held back because a peer that drops for a minute resolves
// itself, and crying about that teaches the operator to ignore the message that
// matters.
func TestMagmaOpenAlertWaitsBeforeSpeakingUp(t *testing.T) {
	if magmaOpenAlertGrace < 5*time.Minute {
		t.Fatalf("alerting sooner than a peer takes to reconnect is noise: %s", magmaOpenAlertGrace)
	}
	if magmaOpenAlertGrace > 15*time.Minute {
		t.Fatalf("a paid channel unopened this long must already have been reported: %s", magmaOpenAlertGrace)
	}
}

// And it repeats while the channel is still owed. Amboss nudges the seller on
// roughly this cadence for the same reason: something pending for hours has to
// stay visible without becoming noise. A single alert at minute eight is lost by
// the time it matters.
func TestMagmaOpenAlertRepeatsWhileTheChannelIsOwed(t *testing.T) {
	if magmaOpenAlertInterval < 15*time.Minute {
		t.Fatalf("repeating this often turns the alert into noise: %s", magmaOpenAlertInterval)
	}
	if magmaOpenAlertInterval > time.Hour {
		t.Fatalf("an unopened paid channel cannot go this long unmentioned: %s", magmaOpenAlertInterval)
	}
	// The window to open runs to about two days, so the reminder has to survive
	// far longer than the grace period that starts it.
	if magmaOpenAlertInterval <= magmaOpenAlertGrace {
		t.Fatal("the repeat must be slower than the first alert, not faster")
	}
}

// The retry gives up only when Amboss does; the alert cadence must not outlive
// that, or the operator is reminded about an order nothing is working on.
func TestMagmaAlertCadenceFitsInsideTheRetryWindow(t *testing.T) {
	if magmaOpenAlertInterval >= magmaOpenRetryFallback {
		t.Fatal("reminders must fit inside the window the order is still being retried in")
	}
}
