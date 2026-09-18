package server

import "testing"

// Order 4edc6750, 2026-09-15. Amboss said "the buyer prepaid this order" at
// 12:29 and the channel open failed one second later. Six hours on, nothing had
// retried it and nothing had said a word - because both the retry sweep and the
// alert sweep filtered on payment_status = SUCCESSFUL_PAYMENT, and that field
// was still null.
//
// It fills in later, after the order has already moved on. So the one field that
// looks like it answers "has the buyer paid" is empty through exactly the window
// where the money is in and the channel is owed - the window those protections
// exist for, and the only window where they matter.
func TestMagmaPaidSignalIsTheStatusNotThePaymentField(t *testing.T) {
	if !magmaBuyerHasPaid("WAITING_FOR_CHANNEL_OPEN") {
		t.Fatal("Amboss only moves an order here after the buyer prepays; this is the paid window")
	}
}

// Before payment there is nothing owed, and retrying or alerting would be acting
// on a debt that does not exist.
func TestMagmaNotPaidBeforeAmbossSaysSo(t *testing.T) {
	for _, status := range []string{
		"WAITING_FOR_SELLER_APPROVAL", "WAITING_FOR_BUYER_PAYMENT", "",
	} {
		if magmaBuyerHasPaid(status) {
			t.Fatalf("%q is not a paid order", status)
		}
	}
}

// And once the funding is out the debt is settled, so these paths must let go.
// Keeping them would retry an order whose channel already exists and keep
// reminding the operator about it.
func TestMagmaPaidWindowClosesOnceTheChannelIsOut(t *testing.T) {
	for _, status := range []string{
		"SELLER_SENT_TRANSACTION", "SELLER_OPENED_CHANNEL", "VALID_CHANNEL_OPENING",
		"CHANNEL_MONITORING_FINISHED",
	} {
		if magmaBuyerHasPaid(status) {
			t.Fatalf("%q already has its channel; the window is over", status)
		}
		if !magmaStatusMeansChannelIsOut(status) {
			t.Fatalf("%q should be recognised as funded", status)
		}
	}
}

// The two questions are adjacent and must not overlap: one asks whether we owe a
// channel, the other whether we already delivered it. An order cannot be in both
// at once, and a status in neither is simply not our problem yet.
func TestMagmaPaidAndFundedAreDisjoint(t *testing.T) {
	for _, status := range []string{
		"WAITING_FOR_SELLER_APPROVAL", "WAITING_FOR_CHANNEL_OPEN", "SELLER_SENT_TRANSACTION",
		"VALID_CHANNEL_OPENING", "SELLER_REJECTED", "BUYER_FAILED_TO_PAY",
	} {
		if magmaBuyerHasPaid(status) && magmaStatusMeansChannelIsOut(status) {
			t.Fatalf("%q cannot both owe a channel and have delivered one", status)
		}
	}
}
