package server

import "testing"

// A buyer walked away from order b0482426 on 2026-08-10 and Amboss moved it to
// BUYER_FAILED_TO_PAY. Our own local_state only ever advances on our actions, so
// it stayed "accepted" - and the dead sale kept reserving 1,000,000 sat of wallet
// balance, 3,068 sat of "promised" revenue, and one of the two concurrent-open
// slots auto mode needs, with no way to ever release them.
//
// The queries over magmaCommittedStates are what read that state, and every one
// of them has to consult the Amboss status too.
func TestMagmaTerminalStatusListCoversTheWholeSet(t *testing.T) {
	list := magmaTerminalStatusList()
	if len(list) != len(magmaTerminalStatuses) {
		t.Fatalf("the slice and the map must not drift: %d vs %d", len(list), len(magmaTerminalStatuses))
	}
	seen := make(map[string]bool, len(list))
	for _, status := range list {
		if !magmaTerminalStatuses[status] {
			t.Fatalf("%q is in the list but not in the map", status)
		}
		if seen[status] {
			t.Fatalf("%q appears twice, which would skew any SQL using it", status)
		}
		seen[status] = true
	}

	// The status that caused this, plus the other ways a sale dies after we have
	// already accepted it and are holding balance for it.
	for _, status := range []string{
		"BUYER_FAILED_TO_PAY",
		"BUYER_REJECTED",
		"ADMIN_CLOSED",
		"SELLER_FAILED_TO_OPEN_CHANNEL",
		"INVALID_CHANNEL_OPENING",
	} {
		if !seen[status] {
			t.Fatalf("%q must release the commitment, otherwise the balance is held forever", status)
		}
	}
}

// The committed states are the ones that reserve balance. They describe our side
// of the deal only, which is exactly why they cannot be trusted on their own.
func TestMagmaCommittedStatesAreLocalOnly(t *testing.T) {
	want := map[string]bool{
		magmaStateAccepting: true,
		magmaStateAccepted:  true,
		magmaStateOpening:   true,
	}
	if len(magmaCommittedStates) != len(want) {
		t.Fatalf("committed states changed: %v", magmaCommittedStates)
	}
	for _, state := range magmaCommittedStates {
		if !want[state] {
			t.Fatalf("%q reserves wallet balance; adding it here needs the terminal-status "+
				"exclusion reviewed in Capacity, PnL and concurrentOpens", state)
		}
		// A local state must never collide with an Amboss status: they are
		// different vocabularies and the queries compare them in separate columns.
		if magmaTerminalStatuses[state] {
			t.Fatalf("%q is both a local state and an Amboss status", state)
		}
	}
}
