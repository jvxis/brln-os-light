package server

import (
	"testing"

	"lightningos-light/internal/lndclient"
)

func policyChannel(base, rate, inBase, inRate int64) lndclient.ChannelInfo {
	return lndclient.ChannelInfo{BaseFeeMsat: &base, FeeRatePpm: &rate, InboundBaseMsat: &inBase, InboundFeeRatePpm: &inRate}
}

// A 1M sat forward through an exit priced at 471 ppm. Numbers are shapes.
func TestComputeForwardPolicySnapshotNoDiscount(t *testing.T) {
	out := policyChannel(0, 471, 0, 0)
	in := policyChannel(0, 829, 0, 0)
	snap, ok := computeForwardPolicySnapshot(1_000_471_000, 1_000_000_000, 471_000, in, true, out, true)
	if !ok || snap.FeePolicyOutMsat != 471_000 || snap.InboundDiscountMsat != 0 {
		t.Fatalf("expected policy 471000 and no discount, got %+v ok=%v", snap, ok)
	}
	if snap.InboundFeeMsat == nil || *snap.InboundFeeMsat != 0 {
		t.Fatalf("inbound component must be recorded as zero, got %v", snap.InboundFeeMsat)
	}
}

func TestComputeForwardPolicySnapshotPartialDiscount(t *testing.T) {
	out := policyChannel(0, 471, 0, 0)
	in := policyChannel(0, 829, 0, -314) // balanced inbound discount on the entry channel
	snap, ok := computeForwardPolicySnapshot(1_000_157_000, 1_000_000_000, 157_000, in, true, out, true)
	if !ok || snap.FeePolicyOutMsat != 471_000 {
		t.Fatalf("unexpected snapshot %+v ok=%v", snap, ok)
	}
	if snap.InboundFeeMsat == nil || *snap.InboundFeeMsat != -314_049 {
		t.Fatalf("expected inbound component -314049, got %v", snap.InboundFeeMsat)
	}
	if snap.InboundDiscountMsat != 314_000 {
		t.Fatalf("expected discount 314000 (policy minus charged), got %d", snap.InboundDiscountMsat)
	}
}

func TestComputeForwardPolicySnapshotDiscountZeroesTheFee(t *testing.T) {
	out := policyChannel(1000, 293, 0, 0)
	in := policyChannel(0, 829, 0, -1085)
	snap, ok := computeForwardPolicySnapshot(1_000_000_000, 1_000_000_000, 0, in, true, out, true)
	if !ok || snap.FeePolicyOutMsat != 294_000 || snap.InboundDiscountMsat != 294_000 {
		t.Fatalf("a zero-fee forward must record the whole policy fee as discount, got %+v ok=%v", snap, ok)
	}
}

func TestComputeForwardPolicySnapshotNeedsOutPolicy(t *testing.T) {
	in := policyChannel(0, 829, 0, -314)
	if _, ok := computeForwardPolicySnapshot(1_000_000_000, 1_000_000_000, 0, in, true, lndclient.ChannelInfo{}, false); ok {
		t.Fatalf("unknown outgoing channel must not produce a snapshot")
	}
	if _, ok := computeForwardPolicySnapshot(1_000_000_000, 1_000_000_000, 0, in, true, lndclient.ChannelInfo{}, true); ok {
		t.Fatalf("outgoing channel without a policy must not produce a snapshot")
	}
	// Unknown incoming channel: the outgoing side is still recorded, the
	// inbound component stays null.
	out := policyChannel(0, 471, 0, 0)
	snap, ok := computeForwardPolicySnapshot(1_000_000_000, 1_000_000_000, 100_000, lndclient.ChannelInfo{}, false, out, true)
	if !ok || snap.InboundFeeMsat != nil || snap.InboundDiscountMsat != 371_000 {
		t.Fatalf("expected snapshot without inbound component, got %+v ok=%v", snap, ok)
	}
}

func TestPolicyFeeMsatMirrorsLND(t *testing.T) {
	if got := policyFeeMsat(1000, 500, 2_000_000_000); got != 1_001_000 {
		t.Fatalf("base 1000 + 2M sat at 500 ppm must be 1001000 msat, got %d", got)
	}
	if got := policyFeeMsat(0, -250, 4_000_000_000); got != -1_000_000 {
		t.Fatalf("negative rate must yield a negative component, got %d", got)
	}
}
