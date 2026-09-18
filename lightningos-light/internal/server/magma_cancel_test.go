package server

import (
	"errors"
	"testing"
)

// The refusal that ended order 4edc6750, verbatim from LND relaying the buyer's
// node. The buyer had ordered 0.0198 BTC from a node that caps channels at 0.01,
// so no attempt could ever have succeeded - the window would have been spent
// retrying an impossibility before recording a seller failure.
func TestMagmaChannelSizeRefusalIsPermanent(t *testing.T) {
	err := errors.New("channel open failed: rpc error: code = Unknown desc = received funding error " +
		"from 0370c19eae3acaaad5b2d570f0925597720472800f2c6fac317aa11c52edd76605: " +
		"chan_id=a463779c6fcbab547c0391f4d58c873ca23bc17908b889c101faea66e9e78107, " +
		"err=chan size of 0.01986390 BTC exceeds maximum chan size of 0.01000000 BTC")
	if !magmaFundingErrorIsPermanent(err) {
		t.Fatal("a node that caps channels below the order will refuse every retry")
	}
}

// Everything transient keeps its retries. Cancelling writes off a paid order, so
// the asymmetry rules: missing a permanent failure costs attempts, which are
// cheap, while calling a transient one permanent throws away a sale that would
// have completed.
func TestMagmaTransientFailuresAreNeverCancelled(t *testing.T) {
	for _, message := range []string{
		"channel open failed: rpc error: code = Unknown desc = peer disconnected",
		"channel open failed: not enough witness outputs to create funding transaction",
		"channel open failed: context deadline exceeded",
		"channel open failed: rpc error: code = Unavailable desc = transport is closing",
		"",
	} {
		if magmaFundingErrorIsPermanent(errors.New(message)) {
			t.Fatalf("%q can succeed on a later attempt and must keep retrying", message)
		}
	}
	if magmaFundingErrorIsPermanent(nil) {
		t.Fatal("no error is not a permanent failure")
	}
}

// The reasons are the enum Amboss accepts, not free text. A typo here would be
// rejected on an order that is already paid, at the moment there is least room
// to recover.
func TestMagmaCancellationReasonsMatchTheAmbossEnum(t *testing.T) {
	if magmaCancelChannelSizeOutOfBounds != "CHANNEL_SIZE_OUT_OF_BOUNDS" {
		t.Fatalf("unexpected reason %q", magmaCancelChannelSizeOutOfBounds)
	}
	if magmaCancelUnableToConnect != "UNABLE_TO_CONNECT_TO_NODE" {
		t.Fatalf("unexpected reason %q", magmaCancelUnableToConnect)
	}
}
