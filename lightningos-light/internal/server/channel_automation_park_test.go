package server

import (
	"testing"

	"lightningos-light/internal/lndclient"
)

func TestParkedInboundPolicyUpdate(t *testing.T) {
	if update, _ := parkedInboundPolicyUpdate(lndclient.ChannelPolicy{FeeRatePpm: 500}); update {
		t.Fatal("no inbound discount: nothing to reset")
	}
	update, rate := parkedInboundPolicyUpdate(lndclient.ChannelPolicy{FeeRatePpm: 500, InboundFeeRatePpm: -120})
	if !update || rate != 0 {
		t.Fatalf("inbound discount must be reset to zero on park, got update=%v rate=%d", update, rate)
	}
	if update, _ := parkedInboundPolicyUpdate(lndclient.ChannelPolicy{InboundBaseMsat: -1000}); !update {
		t.Fatal("negative inbound base fee is a discount too")
	}
}
