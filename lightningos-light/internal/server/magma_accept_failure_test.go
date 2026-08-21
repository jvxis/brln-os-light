package server

import (
	"testing"
	"time"
)

// Order 1ad98d50 was created 2026-08-20 21:19:33 and answered every 95 seconds
// for two hours: roughly 75 identical attempts, each refused by Amboss with
// "Unable to find a route to this destination". The node had 504M sat of inbound
// across 93 public channels at the time and the previous order of the same shape
// had been accepted 36 hours earlier, so the blocker was not ours to clear.
// Amboss recorded SELLER_FAILED_TO_REACT at 23:21.
//
// The approval deadline already existed, but only on the policy branch - it
// judged whether an order was worth taking, never whether taking it kept
// failing. So a decision to accept that could not be carried out retried
// forever, which is the one outcome that loses the sale AND the seller record.
func TestMagmaFailedAcceptRefusesOnceTheWindowIsSpent(t *testing.T) {
	// The first attempts, minutes in: the failure may well be transient.
	if v := classifyMagmaAcceptFailure(1*time.Minute, true); v != magmaAcceptRetry {
		t.Fatalf("a fresh failure must be retried, got %v", v)
	}
	if v := classifyMagmaAcceptFailure(magmaApprovalGrace-time.Second, true); v != magmaAcceptRetry {
		t.Fatalf("one second before the grace period must still retry, got %v", v)
	}

	// Past the grace period the retry is no longer a retry: it is waiting for the
	// window to shut. The sale is gone either way, so refuse and keep the record.
	if v := classifyMagmaAcceptFailure(magmaApprovalGrace, true); v != magmaAcceptRefuse {
		t.Fatalf("at the grace period the order must be refused explicitly, got %v", v)
	}
	if v := classifyMagmaAcceptFailure(2*time.Hour, true); v != magmaAcceptRefuse {
		t.Fatalf("well past the window the order must still be refused, got %v", v)
	}
}

// An unknown creation time gives PendingFor == 0. Refusing then would be
// refusing on a guessed age, so the order keeps its chances.
func TestMagmaFailedAcceptWithUnknownAgeKeepsRetrying(t *testing.T) {
	if v := classifyMagmaAcceptFailure(0, true); v != magmaAcceptRetry {
		t.Fatalf("an order of unknown age must not be refused, got %v", v)
	}
}

// Auto-rejection off is the operator's call, but it is not free. The order is
// left to lapse and the coming seller failure is stated rather than filed as a
// routine retry - being invisible until too late is what made 1ad98d50 hurt.
func TestMagmaFailedAcceptWithAutoRejectOffOnlyWarns(t *testing.T) {
	if v := classifyMagmaAcceptFailure(magmaApprovalGrace+time.Minute, false); v != magmaAcceptWarnOnly {
		t.Fatalf("with auto-reject off the deadline must warn, not reject, got %v", v)
	}
	if v := classifyMagmaAcceptFailure(time.Minute, false); v != magmaAcceptRetry {
		t.Fatalf("before the deadline there is nothing to warn about yet, got %v", v)
	}
}

// The remaining-window figure goes into an operator-facing message, so a pass
// that runs after the window closed must not announce negative minutes.
func TestMagmaWindowRemainingFloorsAtZero(t *testing.T) {
	if got := magmaWindowRemaining(magmaApprovalWindow + time.Hour); got != 0 {
		t.Fatalf("expected 0 once the window has closed, got %s", got)
	}
	if got := magmaWindowRemaining(magmaApprovalWindow); got != 0 {
		t.Fatalf("expected 0 exactly at the window, got %s", got)
	}
	if got := magmaWindowRemaining(magmaApprovalGrace); got != magmaApprovalWindow-magmaApprovalGrace {
		t.Fatalf("expected the real remainder inside the window, got %s", got)
	}
}
