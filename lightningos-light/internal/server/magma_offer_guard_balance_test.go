package server

import "testing"

// On 2026-08-15 order afd053c7 was accepted, the funding transaction went out,
// and one minute later the guard took a healthy offer off the market:
//
//	06:27  on-chain sent 2,055,140 sat (channel 2,054,985 + fee 155)
//	06:28  offer disabled - required 8,171,425, available 4,643,013
//	06:30  funding transaction confirmed
//	06:32  offer re-enabled  - required 8,171,425, available 9,487,188
//
// Nothing about the wallet had actually changed. The inputs had left the
// confirmed balance and the change had not landed yet, so 4,844,175 sat of our
// own money was invisible for those four minutes.
func TestOfferGuardMustNotReactToOurOwnPendingChange(t *testing.T) {
	const (
		confirmedBeforeTx = int64(11_542_328)
		spentOnFunding    = int64(2_055_140) // channel plus the mining fee
		changeReturning   = int64(4_844_175)
		offerRemaining    = int64(8_071_425)
		reserve           = int64(100_000)
	)

	// The reconstruction has to close, otherwise the diagnosis is a guess.
	if confirmedBeforeTx-spentOnFunding != 9_487_188 {
		t.Fatalf("balance after confirmation should be 9,487,188, got %d",
			confirmedBeforeTx-spentOnFunding)
	}
	inputsSpent := changeReturning + spentOnFunding
	if confirmedBeforeTx-inputsSpent != 4_643_013 {
		t.Fatalf("balance while unconfirmed should be 4,643,013, got %d",
			confirmedBeforeTx-inputsSpent)
	}

	// Confirmed-only is what produced the false alarm.
	confirmedOnly := confirmedBeforeTx - inputsSpent
	if confirmedOnly-reserve >= offerRemaining {
		t.Fatal("this window has to look like a shortfall; otherwise the test " +
			"is not reproducing the incident")
	}

	// Counting the pending change removes it: the offer covers itself the whole
	// time, which is the truth.
	withPendingChange := confirmedOnly + changeReturning
	if withPendingChange-reserve < offerRemaining {
		t.Fatalf("with the change counted the offer stays covered: %d available "+
			"against %d needed", withPendingChange-reserve, offerRemaining)
	}
}

// The window where the balance lies is exactly the one the previous state list
// left out: the funding transaction is broadcast but not yet confirmed.
func TestSaleInFlightCoversTheBroadcastWindow(t *testing.T) {
	inFlight := make(map[string]bool, len(magmaSaleInFlightStates))
	for _, state := range magmaSaleInFlightStates {
		inFlight[state] = true
	}

	for _, state := range []string{
		magmaStateAccepting, magmaStateAccepted, magmaStateOpening,
		magmaStateOpenBroadcast, magmaStateConfirming,
	} {
		if !inFlight[state] {
			t.Errorf("%s is part of a live sale; the guard must stand still", state)
		}
	}
	// open_broadcast and confirming are the whole point: magmaCommittedStates
	// stops at opening, one step before the balance starts lying.
	committed := make(map[string]bool, len(magmaCommittedStates))
	for _, state := range magmaCommittedStates {
		committed[state] = true
	}
	for _, state := range []string{magmaStateOpenBroadcast, magmaStateConfirming} {
		if committed[state] {
			t.Fatalf("%s is unexpectedly in magmaCommittedStates; this test's premise is stale", state)
		}
		if !inFlight[state] {
			t.Errorf("%s must be treated as a sale in flight", state)
		}
	}

	// An order that never got anywhere must not freeze the guard forever.
	if inFlight[magmaStateObserved] || inFlight[magmaStateRejected] {
		t.Error("observed and rejected orders hold no funds and must not block the guard")
	}

	// The trap: magmaStateConfirming and magmaStateConfirmed differ by one
	// letter. "confirming" is the broadcast window and belongs here; "confirmed"
	// is a sold channel serving out its commitment, which lasts up to 60 days.
	// Including it would freeze the guard for the whole of that period, once per
	// sale, and the balance would stop being watched exactly when there is more
	// to watch.
	if inFlight[magmaStateConfirmed] {
		t.Fatal("magmaStateConfirmed is a live channel under commitment, not a sale " +
			"in flight; including it would disable the guard for the whole commitment")
	}
}
