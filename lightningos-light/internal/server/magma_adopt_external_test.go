package server

import "testing"

// On 2026-08-21 order 109a4760 was approved through the Amboss web UI after the
// API had refused the invoice ~30 times. Amboss moved it to
// WAITING_FOR_CHANNEL_OPEN and the buyer paid, but this app still had it as
// `observed` - and funding requires `accepted`, so the app refused to open a
// channel that was already paid for. The only fix at the time was an UPDATE by
// hand. Left alone it would have become SELLER_FAILED_TO_OPEN_CHANNEL, which is
// worse than the refusal the automation was trying to avoid.
func TestMagmaAdoptsOrderApprovedOutsideTheApp(t *testing.T) {
	if !magmaShouldAdoptExternalAccept(magmaStateObserved, "WAITING_FOR_CHANNEL_OPEN") {
		t.Fatal("an order Amboss reports as paid, that we never accepted, must be adopted")
	}
}

// Every other local state already belongs to a recovery path of its own, so
// adopting from one would overwrite real progress with a guess. `accepting` in
// particular has its own reconcile branch that asks Amboss which way the
// interrupted accept actually went.
func TestMagmaAdoptionLeavesOtherStatesAlone(t *testing.T) {
	for _, state := range []string{
		magmaStateAccepting, magmaStateAccepted, magmaStateOpening,
		magmaStateOpenBroadcast, magmaStateConfirming, magmaStateConfirmed,
		magmaStateRejected, magmaStateNeedsAttention,
	} {
		if magmaShouldAdoptExternalAccept(state, "WAITING_FOR_CHANNEL_OPEN") {
			t.Fatalf("local state %s owns itself and must not be adopted", state)
		}
	}
}

// Adoption means "the buyer has paid and we owe a channel". Any earlier status
// means no such debt exists yet, and promoting on one would mark an order as
// accepted that nobody has accepted.
func TestMagmaAdoptionRequiresThePaidStatus(t *testing.T) {
	for _, status := range []string{
		"WAITING_FOR_SELLER_APPROVAL", "SELLER_FAILED_TO_REACT", "SELLER_REJECTED",
		"VALID_CHANNEL_OPENING", "CHANNEL_MONITORING_FINISHED", "",
	} {
		if magmaShouldAdoptExternalAccept(magmaStateObserved, status) {
			t.Fatalf("status %q does not mean the buyer has paid", status)
		}
	}
}
