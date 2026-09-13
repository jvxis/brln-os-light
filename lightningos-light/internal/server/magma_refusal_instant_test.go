package server

import (
	"testing"
	"time"
)

// Order a346c26f, 2026-09-12: created 10:50:39, Amboss deadline 12:50:39, and
// the buyer's node unreachable from two independent nodes. The UI told the
// operator only that "Amboss records a failure if you let these expire" - a risk
// the app no longer takes, and no indication of when it would act. The number
// that matters is when auto mode refuses, because that is the operator's window
// to do something else instead.
func TestMagmaRefusalInstantUsesAmbossDeadline(t *testing.T) {
	created := time.Date(2026, 9, 12, 10, 50, 39, 0, time.UTC)
	deadline := time.Date(2026, 9, 12, 12, 50, 39, 0, time.UTC)
	order := MagmaOrder{
		Status: "WAITING_FOR_SELLER_APPROVAL", LocalState: magmaStateObserved,
		CreatedAt: &created, TimeoutAt: &deadline,
	}
	at := magmaRefusalInstant(order)
	if at == nil {
		t.Fatal("an order still waiting on us has an instant we will act on")
	}
	// Deliberately before the deadline: refusing at it is not refusing, it is the
	// lapse Amboss records as SELLER_FAILED_TO_REACT.
	if !at.Before(deadline) {
		t.Fatalf("the refusal must leave room before the deadline, got %s vs %s", at, deadline)
	}
	want := time.Date(2026, 9, 12, 12, 10, 39, 0, time.UTC)
	if !at.Equal(want) {
		t.Fatalf("expected %s, got %s", want, at)
	}
}

// Without a published deadline the estimate still applies, so the countdown does
// not vanish on orders Amboss gives no instant for.
func TestMagmaRefusalInstantFallsBackToCreation(t *testing.T) {
	created := time.Date(2026, 9, 12, 10, 50, 39, 0, time.UTC)
	order := MagmaOrder{
		Status: "WAITING_FOR_SELLER_APPROVAL", LocalState: magmaStateObserved,
		CreatedAt: &created,
	}
	at := magmaRefusalInstant(order)
	if at == nil || !at.Equal(created.Add(magmaApprovalGrace)) {
		t.Fatalf("expected the grace period from creation, got %v", at)
	}
}

// Nothing is shown when nothing is pending on our side, and nothing is invented
// when the order's age is unknown - the policy does not refuse in that case
// either, and a countdown to a made-up moment is worse than none.
func TestMagmaRefusalInstantStaysSilentWhenItShould(t *testing.T) {
	created := time.Date(2026, 9, 12, 10, 50, 39, 0, time.UTC)
	for name, order := range map[string]MagmaOrder{
		"already accepted": {Status: "WAITING_FOR_SELLER_APPROVAL", LocalState: magmaStateAccepted, CreatedAt: &created},
		"buyer has paid":   {Status: "WAITING_FOR_CHANNEL_OPEN", LocalState: magmaStateObserved, CreatedAt: &created},
		"age unknown":      {Status: "WAITING_FOR_SELLER_APPROVAL", LocalState: magmaStateObserved},
	} {
		if at := magmaRefusalInstant(order); at != nil {
			t.Fatalf("%s: expected no countdown, got %s", name, at)
		}
	}
}
