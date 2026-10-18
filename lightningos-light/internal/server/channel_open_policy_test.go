package server

import "testing"

func TestParseChannelOpenPolicy(t *testing.T) {
	policy, err := parseChannelOpenPolicy(nil, nil)
	if err != nil || !policy.IsZero() {
		t.Fatalf("blank fields keep the node defaults, got %+v %v", policy, err)
	}
	fee := int64(327)
	base := int64(0)
	policy, err = parseChannelOpenPolicy(&fee, &base)
	if err != nil || policy.FeeRatePpm == nil || *policy.FeeRatePpm != 327 || policy.BaseFeeMsat == nil || *policy.BaseFeeMsat != 0 {
		t.Fatalf("explicit values must survive, including a 0 base fee: %+v %v", policy, err)
	}
	negative := int64(-1)
	if _, err := parseChannelOpenPolicy(&negative, nil); err == nil {
		t.Fatal("negative fee rate must be rejected")
	}
	huge := channelOpenMaxFeeRatePpm + 1
	if _, err := parseChannelOpenPolicy(&huge, nil); err == nil {
		t.Fatal("fee rate above 100% must be rejected")
	}
	if _, err := parseChannelOpenPolicy(nil, &negative); err == nil {
		t.Fatal("negative base fee must be rejected")
	}
	fields := channelOpenPolicyAudit(map[string]any{"private": false}, policy)
	if fields["fee_rate_ppm"] != int64(327) || fields["base_fee_msat"] != int64(0) || fields["private"] != false {
		t.Fatalf("audit payload must carry the requested policy: %+v", fields)
	}
}
