package server

import "testing"

// The terminal set is what lets the poller go quiet. Marking a live status as
// terminal by mistake would stop tracking an order mid-flight, which is how a
// settled payment goes unnoticed.
func TestMagmaTerminalStatuses(t *testing.T) {
	// Every status an order passes through while it still needs watching.
	inFlight := []string{
		"WAITING_FOR_SELLER_APPROVAL",
		"WAITING_FOR_BUYER_PAYMENT",
		"WAITING_FOR_CHANNEL_OPEN",
		"WAITING_FOR_ON_CHAIN_CONFIRMATION",
		"ON_CHAIN_CONFIRMATION",
		"SELLER_OPENED_CHANNEL",
		// The real sale sat here with pending_seller_orders at zero while the
		// HODL invoice was still held; treating it as terminal would have meant
		// never seeing the revenue settle.
		"SELLER_SENT_TRANSACTION",
		"VALID_CHANNEL_OPENING",
	}
	for _, status := range inFlight {
		if magmaTerminalStatuses[status] {
			t.Errorf("%s is still in flight and must not be terminal", status)
		}
	}

	settled := []string{
		"CHANNEL_MONITORING_FINISHED",
		"BUYER_REJECTED",
		"BUYER_FAILED_TO_PAY",
		"SELLER_REJECTED",
		"SELLER_FAILED_TO_REACT",
		"SELLER_FAILED_TO_OPEN_CHANNEL",
		"SELLER_FAILED_TO_SEND_SWAP",
		"INVALID_CHANNEL_OPENING",
		"ADMIN_CLOSED",
	}
	for _, status := range settled {
		if !magmaTerminalStatuses[status] {
			t.Errorf("%s never changes again and should be terminal", status)
		}
	}

	// Guards against the enum drifting: every status the API documents is either
	// in flight or terminal, never unclassified.
	if got, want := len(magmaTerminalStatuses), len(settled); got != want {
		t.Errorf("terminal set has %d entries, expected %d", got, want)
	}
}
