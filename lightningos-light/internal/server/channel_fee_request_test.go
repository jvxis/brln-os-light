package server

import (
	"encoding/json"
	"testing"
)

// The request the Lightning Ops fee card sends. Every fee is a pointer so that
// "the operator left this blank" and "the operator typed 0" stay distinguishable
// all the way to LND.
type channelFeeRequestForTest struct {
	ChannelPoint      string `json:"channel_point"`
	ApplyAll          bool   `json:"apply_all"`
	BaseFeeMsat       *int64 `json:"base_fee_msat"`
	FeeRatePpm        *int64 `json:"fee_rate_ppm"`
	TimeLockDelta     *int64 `json:"time_lock_delta"`
	InboundEnabled    bool   `json:"inbound_enabled"`
	InboundBaseMsat   *int64 `json:"inbound_base_msat"`
	InboundFeeRatePpm *int64 `json:"inbound_fee_rate_ppm"`
}

// resolveFee is the rule the handler applies per channel: an omitted field keeps
// whatever the channel already has.
func resolveFeeForTest(requested *int64, current int64) int64 {
	if requested == nil {
		return current
	}
	return *requested
}

// The operator set a 1 sat base fee on every channel and left the rate blank.
// Decoding into a plain int64 turned that blank into 0, and 0 ppm was written to
// every channel on the node - routing for free until someone noticed.
func TestBlankFeeFieldKeepsTheChannelValue(t *testing.T) {
	var req channelFeeRequestForTest
	body := `{"apply_all":true,"base_fee_msat":1000,"inbound_enabled":false}`
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if req.BaseFeeMsat == nil || *req.BaseFeeMsat != 1000 {
		t.Fatal("the base fee was filled in and must come through")
	}
	if req.FeeRatePpm != nil {
		t.Fatalf("a field absent from the body must decode to nil, got %d", *req.FeeRatePpm)
	}
	if req.TimeLockDelta != nil {
		t.Fatal("the time lock was not filled in; LND needs a value but it comes from the channel")
	}

	// Against a channel currently at 850 ppm with a 40 block delta.
	const currentRate, currentDelta, currentBase = int64(850), int64(40), int64(0)
	if got := resolveFeeForTest(req.FeeRatePpm, currentRate); got != currentRate {
		t.Fatalf("blank rate must keep %d ppm, got %d", currentRate, got)
	}
	if got := resolveFeeForTest(req.TimeLockDelta, currentDelta); got != currentDelta {
		t.Fatalf("blank time lock must keep %d, got %d", currentDelta, got)
	}
	if got := resolveFeeForTest(req.BaseFeeMsat, currentBase); got != 1000 {
		t.Fatalf("the filled base fee must be applied, got %d", got)
	}
}

// Zeroing a fee has to stay possible - it just has to be asked for.
func TestExplicitZeroStillSetsZero(t *testing.T) {
	var req channelFeeRequestForTest
	if err := json.Unmarshal([]byte(`{"channel_point":"abc:0","fee_rate_ppm":0}`), &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.FeeRatePpm == nil {
		t.Fatal("an explicit 0 is a value, not an omission")
	}
	if got := resolveFeeForTest(req.FeeRatePpm, 850); got != 0 {
		t.Fatalf("explicit 0 must win over the current 850 ppm, got %d", got)
	}
}

// The UI sends undefined for blank inputs, which JSON.stringify drops entirely.
// This is the wire shape the browser actually produces.
func TestOmittedFieldsDecodeAsUntouched(t *testing.T) {
	var req channelFeeRequestForTest
	if err := json.Unmarshal([]byte(`{"apply_all":true,"fee_rate_ppm":250}`), &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for name, value := range map[string]*int64{
		"base_fee_msat":        req.BaseFeeMsat,
		"time_lock_delta":      req.TimeLockDelta,
		"inbound_base_msat":    req.InboundBaseMsat,
		"inbound_fee_rate_ppm": req.InboundFeeRatePpm,
	} {
		if value != nil {
			t.Fatalf("%s was not sent and must stay nil, got %d", name, *value)
		}
	}
	if req.FeeRatePpm == nil || *req.FeeRatePpm != 250 {
		t.Fatal("the one field that was sent must survive")
	}
}

// null is what an explicit "no opinion" looks like on the wire; it must not be
// read as zero either.
func TestNullDecodesAsUntouched(t *testing.T) {
	var req channelFeeRequestForTest
	if err := json.Unmarshal([]byte(`{"channel_point":"abc:0","fee_rate_ppm":null,"base_fee_msat":1000}`), &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.FeeRatePpm != nil {
		t.Fatalf("null must decode to nil, got %d", *req.FeeRatePpm)
	}
	if got := resolveFeeForTest(req.FeeRatePpm, 700); got != 700 {
		t.Fatalf("null must keep the current 700 ppm, got %d", got)
	}
}
