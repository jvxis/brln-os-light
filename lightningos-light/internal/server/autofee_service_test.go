package server

import (
	"math"
	"testing"
)

func TestClassifyHTLCFailurePolicy(t *testing.T) {
	entry := htlcManagerFailedEntry{FailureDetail: "AMOUNT BELOW MINIMUM"}
	policy, liquidity := classifyHTLCFailure(entry)
	if !policy {
		t.Fatalf("expected policy=true")
	}
	if liquidity {
		t.Fatalf("expected liquidity=false")
	}
}

func TestClassifyHTLCFailureLiquidity(t *testing.T) {
	entry := htlcManagerFailedEntry{FailureDetail: "TEMPORARY CHANNEL FAILURE"}
	policy, liquidity := classifyHTLCFailure(entry)
	if policy {
		t.Fatalf("expected policy=false")
	}
	if !liquidity {
		t.Fatalf("expected liquidity=true")
	}
}

func TestClassifyHTLCFailureUnknown(t *testing.T) {
	entry := htlcManagerFailedEntry{FailureDetail: "SOMETHING UNKNOWN"}
	policy, liquidity := classifyHTLCFailure(entry)
	if policy || liquidity {
		t.Fatalf("expected policy=false and liquidity=false")
	}
}

func TestForwardRateThresholdScalesWithThresholdFactor(t *testing.T) {
	base := applyHTLCGlobalRateFactor(0.16*htlcForwardSoftRateFactor, htlcGlobalRateFactor, htlcForwardSoftRateFloor)
	if math.Abs(base-0.10) > 0.000001 {
		t.Fatalf("unexpected base threshold: got %.6f want 0.10", base)
	}
	scaled := applyHTLCGlobalRateFactor(base, 1.20, htlcForwardSoftRateFloor)
	if math.Abs(scaled-0.12) > 0.000001 {
		t.Fatalf("unexpected scaled threshold: got %.6f want 0.12", scaled)
	}
}

func TestShouldHoldUpOnRecentRebalance(t *testing.T) {
	if !shouldHoldUpOnRecentRebalance("sink", 0.05, 0.10, 1) {
		t.Fatalf("expected hold-up=true for sink with low out ratio and recent rebalance")
	}
	if shouldHoldUpOnRecentRebalance("sink", 0.20, 0.10, 1) {
		t.Fatalf("expected hold-up=false when out ratio is healthy")
	}
	if shouldHoldUpOnRecentRebalance("router", 0.05, 0.10, 1) {
		t.Fatalf("expected hold-up=false for non-sink channels")
	}
	if shouldHoldUpOnRecentRebalance("sink", 0.05, 0.10, 0) {
		t.Fatalf("expected hold-up=false without recent rebalance")
	}
}

func TestBlendTargetWithSeed(t *testing.T) {
	base := 1000
	blended := blendTargetWithSeed(base, 800, 0.20)
	if blended != 960 {
		t.Fatalf("unexpected blend value: got %d want 960", blended)
	}
	if keep := blendTargetWithSeed(base, 0, 0.20); keep != base {
		t.Fatalf("expected base when seed is missing: got %d want %d", keep, base)
	}
	if keep := blendTargetWithSeed(base, 900, 0); keep != base {
		t.Fatalf("expected base when weight is zero: got %d want %d", keep, base)
	}
}

func TestEffectiveLowOutThresholdsDrainedVsFull(t *testing.T) {
	lowDrained, protectDrained, factorDrained := effectiveLowOutThresholds(0.10, 0.10, "drained", 0.20)
	lowFull, protectFull, factorFull := effectiveLowOutThresholds(0.10, 0.10, "full", 0.80)

	if !(lowDrained > 0.10 && protectDrained > 0.10 && factorDrained > 1.0) {
		t.Fatalf("expected drained calibration to increase thresholds: low=%.4f protect=%.4f factor=%.4f", lowDrained, protectDrained, factorDrained)
	}
	if !(lowFull < 0.10 && protectFull < 0.10 && factorFull < 1.0) {
		t.Fatalf("expected full calibration to decrease thresholds: low=%.4f protect=%.4f factor=%.4f", lowFull, protectFull, factorFull)
	}
}

func TestEffectiveLowOutThresholdsUsesRatioGradient(t *testing.T) {
	lowNearBoundary, _, _ := effectiveLowOutThresholds(0.10, 0.10, "drained", 0.24)
	lowVeryDrained, _, _ := effectiveLowOutThresholds(0.10, 0.10, "drained", 0.05)
	if !(lowVeryDrained > lowNearBoundary) {
		t.Fatalf("expected stronger threshold for lower local ratio: near=%.4f very=%.4f", lowNearBoundary, lowVeryDrained)
	}
}

func TestEffectiveLowOutThresholdsBalancedBias(t *testing.T) {
	low, protect, factor := effectiveLowOutThresholds(0.10, 0.10, "balanced", 0.26)
	if !(low < 0.10 && protect < 0.10 && factor < 1.0) {
		t.Fatalf("expected balanced calibration to be slightly less aggressive: low=%.4f protect=%.4f factor=%.4f", low, protect, factor)
	}
}

func TestEffectiveLowOutThresholdsFallbackAndClamp(t *testing.T) {
	low, protect, factor := effectiveLowOutThresholds(0, 0, "drained", 0.0)
	if low < lowOutThreshMin || low > lowOutThreshMax {
		t.Fatalf("unexpected low threshold clamp: %.4f", low)
	}
	if protect < lowOutThreshMin || protect > lowOutThreshMax {
		t.Fatalf("unexpected protect threshold clamp: %.4f", protect)
	}
	if factor < lowOutFactorMin || factor > lowOutFactorMax {
		t.Fatalf("unexpected factor clamp: %.4f", factor)
	}
}

func TestMinStagnationRecoveryOutSat(t *testing.T) {
	smallCap := int64(1_000_000)
	if got := minStagnationRecoveryOutSat(smallCap); got != stagnationExitMinOutSat1d {
		t.Fatalf("unexpected min recovery for small channel: got %d want %d", got, stagnationExitMinOutSat1d)
	}

	bigCap := int64(50_000_000)
	wantBig := int64(250_000) // 0.5% of capacity.
	if got := minStagnationRecoveryOutSat(bigCap); got != wantBig {
		t.Fatalf("unexpected min recovery for big channel: got %d want %d", got, wantBig)
	}
}

func TestHasStagnationRecoveryFlow(t *testing.T) {
	capSat := int64(10_000_000)
	minOut := minStagnationRecoveryOutSat(capSat)

	if hasStagnationRecoveryFlow(stagnationExitMinFwds1d-1, minOut, capSat) {
		t.Fatalf("expected false when forward count is below threshold")
	}
	if hasStagnationRecoveryFlow(stagnationExitMinFwds1d, minOut-1, capSat) {
		t.Fatalf("expected false when outbound volume is below threshold")
	}
	if !hasStagnationRecoveryFlow(stagnationExitMinFwds1d, minOut, capSat) {
		t.Fatalf("expected true when both flow thresholds are met")
	}
}

func TestHasOutFallback21dSignal(t *testing.T) {
	capSat := int64(10_000_000)
	minOut := minOutFallback21dSat(capSat)

	if hasOutFallback21dSignal(outFallback21dMinFwds-1, minOut, capSat) {
		t.Fatalf("expected false when 21d forward count is below threshold")
	}
	if hasOutFallback21dSignal(outFallback21dMinFwds, minOut-1, capSat) {
		t.Fatalf("expected false when 21d outbound amount is below threshold")
	}
	if !hasOutFallback21dSignal(outFallback21dMinFwds, minOut, capSat) {
		t.Fatalf("expected true when 21d fallback quality thresholds are met")
	}
}

func TestHasRebalFallback21dSignal(t *testing.T) {
	capSat := int64(10_000_000)
	minAmt := minRebalFallback21dSat(capSat)

	if hasRebalFallback21dSignal(minAmt-1, capSat) {
		t.Fatalf("expected false when 21d rebalance amount is below threshold")
	}
	if !hasRebalFallback21dSignal(minAmt, capSat) {
		t.Fatalf("expected true when 21d rebalance amount threshold is met")
	}
}

func TestHasSurgeConfirmSignal(t *testing.T) {
	capSat := int64(10_000_000)
	minAmtSat := minSurgeConfirmRebalSat(capSat)

	if hasSurgeConfirmSignal(0, minAmtSat, capSat) {
		t.Fatalf("expected false without recent rebalance touches")
	}
	if hasSurgeConfirmSignal(1, minAmtSat-1, capSat) {
		t.Fatalf("expected false when rebalance amount is below channel-size threshold")
	}
	if !hasSurgeConfirmSignal(1, minAmtSat, capSat) {
		t.Fatalf("expected true when rebalance amount meets channel-size threshold")
	}
}

func TestMinSurgeConfirmRebalSat(t *testing.T) {
	capSat := int64(20_000_000)
	want := int64(300_000) // 1.5% of capacity.
	if got := minSurgeConfirmRebalSat(capSat); got != want {
		t.Fatalf("unexpected minimum surge confirmation amount: got %d want %d", got, want)
	}
	if got := minSurgeConfirmRebalSat(0); got != 0 {
		t.Fatalf("expected zero threshold when capacity is unknown: got %d", got)
	}
}

func TestMinFloorRebalSat(t *testing.T) {
	capSat := int64(20_000_000)
	want := int64(300_000) // 1.5% of capacity.
	if got := minFloorRebalSat(capSat); got != want {
		t.Fatalf("unexpected minimum floor rebalance amount: got %d want %d", got, want)
	}
	if got := minFloorRebalSat(0); got != 0 {
		t.Fatalf("expected zero threshold when capacity is unknown: got %d", got)
	}
}

func TestHasFloorRebalSignal(t *testing.T) {
	capSat := int64(10_000_000)
	minAmtSat := minFloorRebalSat(capSat)

	if hasFloorRebalSignal(0, capSat) {
		t.Fatalf("expected false without rebalance volume")
	}
	if hasFloorRebalSignal(minAmtSat-1, capSat) {
		t.Fatalf("expected false when rebalance amount is below floor threshold")
	}
	if !hasFloorRebalSignal(minAmtSat, capSat) {
		t.Fatalf("expected true when rebalance amount meets floor threshold")
	}
	if !hasFloorRebalSignal(1, 0) {
		t.Fatalf("expected true with positive amount when capacity is unknown")
	}
}

func TestApplySurgeConfirmationGate(t *testing.T) {
	st := &autofeeChannelState{}

	st.ExplorerState.SurgeGateRounds = 3
	st.ExplorerState.SurgeGatePpm = 1200
	target, tag := applySurgeConfirmationGate(st, 1200, 1180, false, false, false)
	if target != 1180 || tag != "" {
		t.Fatalf("unexpected non-surge result: target=%d tag=%q", target, tag)
	}
	if st.ExplorerState.SurgeGateRounds != 0 || st.ExplorerState.SurgeGatePpm != 0 {
		t.Fatalf("expected surge gate state reset when surge is inactive")
	}

	target, tag = applySurgeConfirmationGate(st, 1000, 1100, true, false, false)
	if target != 1000 || tag != "surge-hold" {
		t.Fatalf("expected first surge round to hold fee: target=%d tag=%q", target, tag)
	}
	if st.ExplorerState.SurgeGateRounds != 1 || st.ExplorerState.SurgeGatePpm != 1000 {
		t.Fatalf("unexpected surge gate state after hold: rounds=%d ppm=%d", st.ExplorerState.SurgeGateRounds, st.ExplorerState.SurgeGatePpm)
	}

	target, tag = applySurgeConfirmationGate(st, 1000, 1100, true, false, false)
	if target != 1000 || tag != "surge-hold-flow" {
		t.Fatalf("expected second surge round without flow confirmation to keep hold: target=%d tag=%q", target, tag)
	}
	if st.ExplorerState.SurgeGateRounds != 2 || st.ExplorerState.SurgeGatePpm != 1000 {
		t.Fatalf("unexpected surge gate state after hold-flow: rounds=%d ppm=%d", st.ExplorerState.SurgeGateRounds, st.ExplorerState.SurgeGatePpm)
	}

	target, tag = applySurgeConfirmationGate(st, 1000, 1100, true, false, true)
	if target != 1100 || tag != "surge-confirmed-rounds" {
		t.Fatalf("expected surge confirmation with flow after minimum rounds: target=%d tag=%q", target, tag)
	}
	if st.ExplorerState.SurgeGateRounds != 0 || st.ExplorerState.SurgeGatePpm != 1000 {
		t.Fatalf("unexpected surge gate state after confirmation: rounds=%d ppm=%d", st.ExplorerState.SurgeGateRounds, st.ExplorerState.SurgeGatePpm)
	}

	st.ExplorerState.SurgeGateRounds = 1
	st.ExplorerState.SurgeGatePpm = 1000
	target, tag = applySurgeConfirmationGate(st, 1000, 1110, true, true, false)
	if target != 1110 || tag != "surge-confirmed" {
		t.Fatalf("expected immediate confirmation with rebalance signal: target=%d tag=%q", target, tag)
	}
	if st.ExplorerState.SurgeGateRounds != 0 || st.ExplorerState.SurgeGatePpm != 1000 {
		t.Fatalf("unexpected surge gate state after signal confirmation: rounds=%d ppm=%d", st.ExplorerState.SurgeGateRounds, st.ExplorerState.SurgeGatePpm)
	}

	st.ExplorerState.SurgeGateRounds = 1
	st.ExplorerState.SurgeGatePpm = 1000
	target, tag = applySurgeConfirmationGate(st, 1050, 1160, true, false, false)
	if target != 1050 || tag != "surge-hold" {
		t.Fatalf("expected hold after local ppm change: target=%d tag=%q", target, tag)
	}
	if st.ExplorerState.SurgeGateRounds != 1 || st.ExplorerState.SurgeGatePpm != 1050 {
		t.Fatalf("unexpected surge gate state after local ppm change: rounds=%d ppm=%d", st.ExplorerState.SurgeGateRounds, st.ExplorerState.SurgeGatePpm)
	}
}

func TestApplyDirectionReversalGuard(t *testing.T) {
	st := &autofeeChannelState{LastDir: "up"}
	localPpm := 1000

	next, tags := applyDirectionReversalGuard(st, localPpm, 900, reversalConfirmMinRounds)
	if next != localPpm {
		t.Fatalf("expected first reversal attempt to be blocked: got %d want %d", next, localPpm)
	}
	if st.ExplorerState.ReversalPendingRounds != 1 || st.ExplorerState.ReversalPendingDir != "down" {
		t.Fatalf("unexpected pending reversal state: rounds=%d dir=%q", st.ExplorerState.ReversalPendingRounds, st.ExplorerState.ReversalPendingDir)
	}
	if len(tags) == 0 || tags[0] != "reversal-guard" {
		t.Fatalf("expected reversal guard tag on first blocked reversal, got %+v", tags)
	}

	next, tags = applyDirectionReversalGuard(st, localPpm, 900, reversalConfirmMinRounds)
	if next != 900 {
		t.Fatalf("expected second reversal attempt to pass guard: got %d want 900", next)
	}
	if st.ExplorerState.ReversalPendingRounds != 2 || st.ExplorerState.ReversalPendingDir != "down" {
		t.Fatalf("expected pending reversal state to be retained after confirmation: rounds=%d dir=%q", st.ExplorerState.ReversalPendingRounds, st.ExplorerState.ReversalPendingDir)
	}
	if len(tags) == 0 || tags[0] != "reversal-confirmed" {
		t.Fatalf("expected reversal confirmation tag, got %+v", tags)
	}

	st.LastDir = "down"
	next, _ = applyDirectionReversalGuard(st, localPpm, 930, reversalConfirmMinRounds)
	if next != 930 {
		t.Fatalf("expected same-direction move to pass without guard: got %d want 930", next)
	}
	if st.ExplorerState.ReversalPendingRounds != 0 || st.ExplorerState.ReversalPendingDir != "" {
		t.Fatalf("expected pending reversal state reset when direction aligns: rounds=%d dir=%q", st.ExplorerState.ReversalPendingRounds, st.ExplorerState.ReversalPendingDir)
	}
}

func TestClassificationBiasEMAUsesFasterWeight(t *testing.T) {
	prevBias := 0.10
	biasRaw := 0.60
	got := (1.0-classificationBiasEMAAlpha)*prevBias + classificationBiasEMAAlpha*biasRaw
	want := 0.40
	if math.Abs(got-want) > 0.000001 {
		t.Fatalf("unexpected EMA weight result: got %.6f want %.6f", got, want)
	}
}

func TestClassifyChannelPromotesSinkSooner(t *testing.T) {
	label, conf := classifyChannel(0.47, 0.14, 2, 3, "router", 0.20)
	if label != "sink" {
		t.Fatalf("expected sink classification, got %q", label)
	}
	if conf <= 0.20 {
		t.Fatalf("expected sink confidence above previous router confidence, got %.4f", conf)
	}
}

func TestClassifyChannelKeepsRouterBandTighter(t *testing.T) {
	label, _ := classifyChannel(0.27, 0.20, 2, 3, "router", 0.55)
	if label != "router" {
		t.Fatalf("expected previous router label to be retained outside tighter router band, got %q", label)
	}

	label, _ = classifyChannel(0.24, 0.20, 2, 3, "", 0)
	if label != "router" {
		t.Fatalf("expected router classification inside tighter router band, got %q", label)
	}
}

func TestClassifyChannelUsesSofterSwitchHysteresis(t *testing.T) {
	label, conf := classifyChannel(0.47, 0.14, 2, 3, "router", 0.25)
	if label != "sink" {
		t.Fatalf("expected sink classification to beat previous router label with softer hysteresis, got %q", label)
	}
	if conf < 0.33 {
		t.Fatalf("expected meaningful sink confidence, got %.4f", conf)
	}
}

func TestApplyDirectionReversalGuardFastTrack(t *testing.T) {
	st := &autofeeChannelState{LastDir: "up"}
	localPpm := 1000

	next, tags := applyDirectionReversalGuard(st, localPpm, 900, 1)
	if next != 900 {
		t.Fatalf("expected first reversal attempt to pass with fast-track: got %d want 900", next)
	}
	if len(tags) < 2 || tags[0] != "reversal-confirmed" || tags[1] != "reversal-fasttrack" {
		t.Fatalf("expected fast-track confirmation tags, got %+v", tags)
	}
}

func TestReversalConfirmRoundsForChannel(t *testing.T) {
	if got := reversalConfirmRoundsForChannel(nil, 80.0); got != reversalConfirmMinRounds {
		t.Fatalf("unexpected rounds for nil state: got %d want %d", got, reversalConfirmMinRounds)
	}

	st := &autofeeChannelState{StalledRounds: reversalFastTrackStallMinRounds - 1}
	if got := reversalConfirmRoundsForChannel(st, 80.0); got != reversalConfirmMinRounds {
		t.Fatalf("unexpected rounds below stall threshold: got %d want %d", got, reversalConfirmMinRounds)
	}

	st.StalledRounds = reversalFastTrackStallMinRounds
	if got := reversalConfirmRoundsForChannel(st, stallAlertGapFrac*100.0-0.1); got != reversalConfirmMinRounds {
		t.Fatalf("unexpected rounds below gap threshold: got %d want %d", got, reversalConfirmMinRounds)
	}
	if got := reversalConfirmRoundsForChannel(st, stallAlertGapFrac*100.0); got != reversalConfirmMinRounds-1 {
		t.Fatalf("unexpected rounds at fast-track threshold: got %d want %d", got, reversalConfirmMinRounds-1)
	}
}

func TestShouldEmitStallAlert(t *testing.T) {
	if shouldEmitStallAlert(stallAlertMinRounds-1, stallAlertGapFrac*100.0+5.0) {
		t.Fatalf("did not expect stall alert below rounds threshold")
	}
	if shouldEmitStallAlert(stallAlertMinRounds, stallAlertGapFrac*100.0-0.1) {
		t.Fatalf("did not expect stall alert below gap threshold")
	}
	if !shouldEmitStallAlert(stallAlertMinRounds, stallAlertGapFrac*100.0) {
		t.Fatalf("expected stall alert at thresholds")
	}
}

func TestCapDownMoveForLowHTLCSample(t *testing.T) {
	localPpm := 1000

	capped, clipped := capDownMoveForLowHTLCSample(localPpm, 850, true)
	if !clipped {
		t.Fatalf("expected clipping for large drop with low sample")
	}
	if capped != 950 {
		t.Fatalf("unexpected clipped value: got %d want 950", capped)
	}

	unchanged, clipped := capDownMoveForLowHTLCSample(localPpm, 980, true)
	if clipped {
		t.Fatalf("did not expect clipping for small drop")
	}
	if unchanged != 980 {
		t.Fatalf("expected unchanged value for small drop: got %d want 980", unchanged)
	}

	unchanged, clipped = capDownMoveForLowHTLCSample(localPpm, 850, false)
	if clipped {
		t.Fatalf("did not expect clipping when sample is not low")
	}
	if unchanged != 850 {
		t.Fatalf("expected unchanged value when sample is not low: got %d want 850", unchanged)
	}
}

func TestCapDownMoveGeneral(t *testing.T) {
	localPpm := 1000

	capped, clipped := capDownMoveGeneral(localPpm, 850, false)
	if !clipped {
		t.Fatalf("expected clipping for large general drop")
	}
	if capped != 920 {
		t.Fatalf("unexpected clipped value: got %d want 920", capped)
	}

	unchanged, clipped := capDownMoveGeneral(localPpm, 950, false)
	if clipped {
		t.Fatalf("did not expect clipping for small drop")
	}
	if unchanged != 950 {
		t.Fatalf("expected unchanged value for small drop: got %d want 950", unchanged)
	}

	unchanged, clipped = capDownMoveGeneral(localPpm, 850, true)
	if clipped {
		t.Fatalf("did not expect general clipping when htlc sample low cap is active")
	}
	if unchanged != 850 {
		t.Fatalf("expected unchanged value when htlc sample low cap is active: got %d want 850", unchanged)
	}
}
