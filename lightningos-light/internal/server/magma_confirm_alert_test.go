package server

import (
	"errors"
	"testing"
)

// Order 7554c21d, 2026-09-02: the funding transaction was broadcast at 17:01:01,
// we tried to report the outpoint at 17:01:03, and Amboss answered "Error
// finding transaction in the mempool" - it had not seen the transaction yet,
// two seconds in. That went to Telegram as a warning, and the retry confirmed
// it 93 seconds later. The sale was never at risk; the alert was pure noise,
// and noise like that trains an operator to ignore the real ones.
func TestMagmaConfirmDoesNotAlertOnTheInlineAttempt(t *testing.T) {
	realFailure := errors.New("outpoint belongs to a different channel")
	if magmaConfirmShouldAlert(true, realFailure) {
		t.Fatal("the attempt made seconds after the broadcast must never alert, whatever Amboss answers")
	}
	// The same error from a retry is worth surfacing: by then the transaction
	// has had time to propagate, so a failure means something.
	if !magmaConfirmShouldAlert(false, realFailure) {
		t.Fatal("a retry failing for a real reason must reach the operator")
	}
}

// The wording that caused the false alarm. Adding it to the propagation check
// keeps retries quiet too - the inline rule above is what makes the alert
// impossible to reintroduce by rewording, but a retry hitting the same race
// should not shout either.
func TestMagmaConfirmRecognisesTheMempoolWording(t *testing.T) {
	for _, message := range []string{
		"Error finding transaction in the mempool",
		"transaction not found",
		"output not found",
		"not found in transaction",
	} {
		if !magmaErrorIsPropagationRace(errors.New(message)) {
			t.Fatalf("%q describes a transaction Amboss cannot see yet", message)
		}
		if magmaConfirmShouldAlert(false, errors.New(message)) {
			t.Fatalf("%q must not alert even on a retry", message)
		}
	}
}

// A wrong outpoint is the failure this alert exists for, and it has to survive
// everything above.
func TestMagmaConfirmStillAlertsOnARealProblem(t *testing.T) {
	if !magmaConfirmShouldAlert(false, errors.New("order already has a channel")) {
		t.Fatal("a genuine refusal from a retry must still reach the operator")
	}
	if magmaConfirmShouldAlert(false, nil) {
		t.Fatal("there is nothing to alert about when the call succeeded")
	}
}
