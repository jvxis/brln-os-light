package server

import (
	"errors"
	"testing"

	"lightningos-light/internal/lndclient"
)

func TestFixedSourceBlockedReason(t *testing.T) {
	const targetID = uint64(100)
	usable := RebalanceChannel{ChannelID: 7, Active: true, MaxSourceSat: 500_000, EligibleAsSource: true}
	excluded := usable
	excluded.ExcludedAsSource = true
	excluded.EligibleAsSource = false

	cases := []struct {
		name          string
		source        RebalanceChannel
		allowExcluded bool
		want          string
	}{
		{"usable", usable, false, ""},
		{"not found", RebalanceChannel{}, true, fixedSourceReasonNotFound},
		{"same channel as target", RebalanceChannel{ChannelID: targetID, Active: true, MaxSourceSat: 1}, true, fixedSourceReasonIsTarget},
		{"inactive", RebalanceChannel{ChannelID: 7, MaxSourceSat: 500_000}, true, fixedSourceReasonInactive},
		{"parked", RebalanceChannel{ChannelID: 7, Active: true, MaxSourceSat: 500_000, AutomationMode: channelAutomationModeParked}, true, fixedSourceReasonParked},
		{"quarantined", RebalanceChannel{ChannelID: 7, Active: true, SourceQuarantined: true}, true, fixedSourceReasonQuarantined},
		{"below the source floor or all liquidity protected", RebalanceChannel{ChannelID: 7, Active: true}, true, fixedSourceReasonNoFreeLiquidity},
		{"excluded without the exception", excluded, false, fixedSourceReasonExcluded},
		{"excluded with the one-job exception", excluded, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fixedSourceBlockedReason(tc.source, targetID, tc.allowExcluded); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}

	// The exception lifts the exclusion only: an excluded channel that also
	// has no free liquidity stays refused, for the liquidity reason.
	drained := excluded
	drained.MaxSourceSat = 0
	if got := fixedSourceBlockedReason(drained, targetID, true); got != fixedSourceReasonNoFreeLiquidity {
		t.Fatalf("exception must not lift the liquidity guard, got %q", got)
	}
}

func TestResolveFixedRebalanceSourceNeverSubstitutes(t *testing.T) {
	snapshots := []RebalanceChannel{
		{ChannelID: 1, Active: true, MaxSourceSat: 900_000, EligibleAsSource: true},
		{ChannelID: 2, Active: true, MaxSourceSat: 100_000, ExcludedAsSource: true},
		{ChannelID: 100, Active: true},
	}
	if got, reason := resolveFixedRebalanceSource(snapshots, 100, 2, true); reason != "" || got.ChannelID != 2 {
		t.Fatalf("excluded source with the exception must resolve to itself, got chan=%d reason=%q", got.ChannelID, reason)
	}
	// Without the exception the better, eligible channel 1 is not picked in
	// its place.
	if got, reason := resolveFixedRebalanceSource(snapshots, 100, 2, false); reason != fixedSourceReasonExcluded || got.ChannelID != 2 {
		t.Fatalf("got chan=%d reason=%q", got.ChannelID, reason)
	}
	if _, reason := resolveFixedRebalanceSource(snapshots, 100, 55, true); reason != fixedSourceReasonNotFound {
		t.Fatalf("closed channel: got %q", reason)
	}

	preview := buildRunPreviewSource(snapshots[1], 100, true)
	if !preview.Eligible || !preview.Excluded || !preview.ExclusionException || preview.MaxSourceSat != 100_000 {
		t.Fatalf("preview of an excepted source: %+v", preview)
	}
	preview = buildRunPreviewSource(snapshots[1], 100, false)
	if preview.Eligible || preview.ExclusionException || preview.BlockedReason != fixedSourceReasonExcluded {
		t.Fatalf("preview of an excluded source: %+v", preview)
	}
}

func TestOperatorFeeCapSharedAcrossLots(t *testing.T) {
	var none *operatorFeeCap
	if got, err := none.clamp(4_000); err != nil || got != 4_000 || none.exhausted() {
		t.Fatalf("no cap must leave the limit alone, got %d %v", got, err)
	}
	none.spendSat(10)

	if newOperatorFeeCap(0) != nil {
		t.Fatalf("zero is not a cap")
	}

	// 12.5 sats over a job that settles in lots.
	feeCap := newOperatorFeeCap(12_500)
	if got, _ := feeCap.clamp(50_000); got != 12_500 {
		t.Fatalf("limit above the cap must be clamped, got %d", got)
	}
	if got, _ := feeCap.clamp(3_000); got != 3_000 {
		t.Fatalf("limit under the cap must pass, got %d", got)
	}
	feeCap.spendSat(4) // charged as 4.999 sats
	if got, _ := feeCap.clamp(50_000); got != 7_501 {
		t.Fatalf("second lot gets what is left, got %d", got)
	}
	feeCap.spendSat(7) // charged as 7.999 sats: more than what is left
	if !feeCap.exhausted() {
		t.Fatalf("cap must be exhausted")
	}
	if got, err := feeCap.clamp(1); !errors.Is(err, errOperatorFeeCapExhausted) || got != 0 {
		t.Fatalf("exhausted cap must refuse the attempt, got %d %v", got, err)
	}
}

func TestCalcFeeLimitMsatHonorsOperatorFeeCap(t *testing.T) {
	policy := lndclient.ChannelPolicySnapshot{FeeRatePpm: 1000}
	cfg := RebalanceConfig{FeeLimitPpm: 500}

	// 25,000 sats at 500 ppm = 12.5 sats.
	if got, err := calcFeeLimitMsat(25_000_000, policy, nil, cfg); err != nil || got != 12_500 {
		t.Fatalf("policy limit: got %d %v", got, err)
	}
	cfg.operatorFeeCap = newOperatorFeeCap(9_000)
	if got, err := calcFeeLimitMsat(25_000_000, policy, nil, cfg); err != nil || got != 9_000 {
		t.Fatalf("absolute cap below the ppm limit must win, got %d %v", got, err)
	}
	// The cap travels with copies of the config.
	copied := cfg
	copied.FeeLimitPpm = 100
	if got, _ := calcFeeLimitMsat(25_000_000, policy, nil, copied); got != 2_500 {
		t.Fatalf("ppm limit below the cap must win, got %d", got)
	}
	cfg.operatorFeeCap.spendSat(9)
	if _, err := calcFeeLimitMsat(25_000_000, policy, nil, copied); !errors.Is(err, errOperatorFeeCapExhausted) {
		t.Fatalf("spent cap must stop further attempts, got %v", err)
	}
}

func TestApplyOperatorFeeCapToPreview(t *testing.T) {
	preview := RebalanceRunPreview{EffectiveAmountSat: 25_000, EffectiveFeeLimitPpm: 500, MaxFeeSat: 13}
	applyOperatorFeeCapToPreview(&preview, 0)
	if preview.MaxFeeMsat != 12_500 || preview.FeeCapAbsolute || preview.MaxFeeSat != 13 {
		t.Fatalf("no cap: %+v", preview)
	}
	applyOperatorFeeCapToPreview(&preview, 20_000)
	if preview.MaxFeeMsat != 12_500 || preview.FeeCapAbsolute {
		t.Fatalf("cap above the ppm limit must not bind: %+v", preview)
	}
	applyOperatorFeeCapToPreview(&preview, 12_000)
	if preview.MaxFeeMsat != 12_000 || !preview.FeeCapAbsolute || preview.MaxFeeSat != 12 {
		t.Fatalf("cap below the ppm limit must bind: %+v", preview)
	}
}

func TestValidateRebalanceRunOverridesOperatorOptions(t *testing.T) {
	zero := int64(0)
	cap := int64(12_500)
	if err := validateRebalanceRunOverrides(rebalanceRunPayload{MaxFeeMsat: &zero}); err == nil {
		t.Fatalf("zero cap must be rejected")
	}
	if err := validateRebalanceRunOverrides(rebalanceRunPayload{AllowExcludedSource: true}); err == nil {
		t.Fatalf("exception without a fixed source must be rejected")
	}
	ok := rebalanceRunPayload{SourceChannelPoint: "aa:0", AllowExcludedSource: true, MaxFeeMsat: &cap}
	if err := validateRebalanceRunOverrides(ok); err != nil {
		t.Fatalf("valid operator options: %v", err)
	}
	if !ok.hasFixedSource() || (rebalanceRunPayload{}).hasFixedSource() {
		t.Fatalf("hasFixedSource")
	}
}
