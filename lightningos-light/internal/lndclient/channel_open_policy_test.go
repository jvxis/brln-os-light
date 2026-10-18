package lndclient

import (
	"testing"

	"lightningos-light/lnrpc"
)

func TestApplyOpenChannelPolicyKeepsNodeDefaultsWhenBlank(t *testing.T) {
	req := &lnrpc.OpenChannelRequest{}
	applyOpenChannelPolicy(req, ChannelOpenPolicy{})
	if req.UseFeeRate || req.UseBaseFee {
		t.Fatalf("blank policy must leave the lnd.conf defaults in place, got %+v", req)
	}
}

func TestApplyOpenChannelPolicyZeroIsAnExplicitValue(t *testing.T) {
	// coinos, 17/10/2026: the second channel had to open at 1791 ppm with no
	// base fee; 0 msat is a value, not a blank.
	fee := int64(1791)
	base := int64(0)
	req := &lnrpc.OpenChannelRequest{}
	applyOpenChannelPolicy(req, ChannelOpenPolicy{FeeRatePpm: &fee, BaseFeeMsat: &base})
	if !req.UseFeeRate || req.FeeRate != 1791 || !req.UseBaseFee || req.BaseFee != 0 {
		t.Fatalf("unexpected request policy: %+v", req)
	}
	batch := &lnrpc.BatchOpenChannel{}
	applyBatchOpenChannelPolicy(batch, ChannelOpenPolicy{FeeRatePpm: &fee})
	if !batch.UseFeeRate || batch.FeeRate != 1791 || batch.UseBaseFee {
		t.Fatalf("batch item must carry only the fields that were set: %+v", batch)
	}
}
