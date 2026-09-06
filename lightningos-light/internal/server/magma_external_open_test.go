package server

import "testing"

// Issue #131. Order fd0a2cd0 was accepted by this app, the auto-open declined,
// the operator opened the 500,000 sat channel by hand, and Amboss moved the
// order to VALID_CHANNEL_OPENING. The local state stayed `accepted`, which is
// one of the states that reserves wallet balance against a channel still owed -
// so the app went on holding 500,000 sat for a channel that was already open,
// and said so on screen. The only way out was an UPDATE by hand.
func TestMagmaStatusMeansChannelIsOutCoversTheReportedCase(t *testing.T) {
	for _, status := range []string{
		"VALID_CHANNEL_OPENING", "SELLER_SENT_TRANSACTION", "SELLER_OPENED_CHANNEL",
		"WAITING_FOR_ON_CHAIN_CONFIRMATION", "ON_CHAIN_CONFIRMATION",
		"CHANNEL_MONITORING_FINISHED",
	} {
		if !magmaStatusMeansChannelIsOut(status) {
			t.Fatalf("%s means the funding is already out, so the order must stop reserving", status)
		}
	}
}

// Before the funding exists the reservation is doing its job: the money is owed
// and has not left. Releasing it here would let two orders spend the same coins.
func TestMagmaStatusBeforeTheChannelKeepsTheReservation(t *testing.T) {
	for _, status := range []string{
		"WAITING_FOR_SELLER_APPROVAL", "WAITING_FOR_BUYER_PAYMENT", "WAITING_FOR_CHANNEL_OPEN", "",
	} {
		if magmaStatusMeansChannelIsOut(status) {
			t.Fatalf("%s still owes a channel, so the balance stays reserved", status)
		}
	}
}

// The terminal list answers "is this order over"; this answers "has the money
// left". They are different questions, and a successful order in flight belongs
// to the second and not the first - which is why the terminal list could not be
// reused here.
func TestMagmaChannelIsOutIsNotTheTerminalList(t *testing.T) {
	if magmaTerminalStatuses["VALID_CHANNEL_OPENING"] {
		t.Fatal("a successful opening must not be treated as a closed order")
	}
	if !magmaStatusMeansChannelIsOut("VALID_CHANNEL_OPENING") {
		t.Fatal("but its funding has left the wallet, so it must release the reservation")
	}
	for status := range magmaTerminalStatuses {
		if status == "INVALID_CHANNEL_OPENING" && magmaStatusMeansChannelIsOut(status) {
			t.Fatal("an invalid opening is a failure, not a funded channel")
		}
	}
}
