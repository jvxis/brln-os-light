package server

import (
	"math"
	"testing"
	"time"
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

func TestApplySeedSoftEnvelopeCapsTargetToCeiling(t *testing.T) {
	got, tags := applySeedSoftEnvelope(900, 400, 1.10, 1.50, true)
	if got != 600 {
		t.Fatalf("expected target capped to seed ceiling: got %d want 600", got)
	}
	if len(tags) != 1 || tags[0] != "seed:soft-ceil" {
		t.Fatalf("expected seed:soft-ceil tag, got %#v", tags)
	}
}

func TestApplySeedSoftEnvelopeRaisesTargetToFloorWhenAllowed(t *testing.T) {
	got, tags := applySeedSoftEnvelope(300, 400, 1.10, 1.50, true)
	if got != 440 {
		t.Fatalf("expected target raised to seed floor: got %d want 440", got)
	}
	if len(tags) != 1 || tags[0] != "seed:soft-floor" {
		t.Fatalf("expected seed:soft-floor tag, got %#v", tags)
	}
}

func TestApplySeedSoftEnvelopeDoesNotRaiseFloorWhenNotAllowed(t *testing.T) {
	got, tags := applySeedSoftEnvelope(300, 400, 1.10, 1.50, false)
	if got != 300 {
		t.Fatalf("expected target unchanged when floor raise is not allowed: got %d want 300", got)
	}
	if len(tags) != 0 {
		t.Fatalf("expected no tags when no envelope adjustment happens, got %#v", tags)
	}
}

func TestComputeInboundDiscountWithRetainedSpread(t *testing.T) {
	got := computeInboundDiscount(true, "sink", 0.12, 6, 300, 500, 1000, 0.95, 0.15, 0.12)
	if got != 379 {
		t.Fatalf("unexpected inbound discount: got %d want 379", got)
	}
}

func TestComputeInboundDiscountRespectsReachOutRatio(t *testing.T) {
	got := computeInboundDiscount(true, "sink", 0.16, 6, 300, 500, 1000, 0.95, 0.15, 0.12)
	if got != 0 {
		t.Fatalf("expected no inbound discount beyond reach ratio, got %d", got)
	}
}

func TestShouldEnableSeedEnvelope(t *testing.T) {
	if !shouldEnableSeedEnvelope(500, true, true, 0, 0, false, false, false, false, false, false, false) {
		t.Fatalf("expected seed envelope when channel has no recent flow and no strong signals")
	}
	if !shouldEnableSeedEnvelope(500, false, true, 1, 0, false, false, false, false, false, false, false) {
		t.Fatalf("expected seed envelope for weak recent flow with minimal 1d forwards")
	}
	if shouldEnableSeedEnvelope(500, false, true, 3, 0, false, false, false, false, false, false, false) {
		t.Fatalf("did not expect seed envelope with meaningful recent forwards")
	}
	if shouldEnableSeedEnvelope(500, true, true, 0, 0, false, true, false, false, false, false, false) {
		t.Fatalf("did not expect seed envelope with strong recent signal")
	}
}

func TestShouldRelaxNegMarginForSeedSoftEnvelope(t *testing.T) {
	if !shouldRelaxNegMarginForSeedSoftEnvelope(true, []string{"seed:soft-ceil"}, 1300, 1100, 760, 1.50) {
		t.Fatalf("expected neg-margin relaxation when soft ceiling is active")
	}
	if shouldRelaxNegMarginForSeedSoftEnvelope(false, []string{"seed:soft-ceil"}, 1300, 1100, 760, 1.50) {
		t.Fatalf("did not expect neg-margin relaxation without active seed envelope")
	}
	if !shouldRelaxNegMarginForSeedSoftEnvelope(true, []string{"seed:soft-floor"}, 1304, 836, 760, 1.50) {
		t.Fatalf("expected neg-margin relaxation when local fee is well above the seed ceiling")
	}
	if shouldRelaxNegMarginForSeedSoftEnvelope(true, []string{"seed:soft-floor"}, 900, 836, 760, 1.50) {
		t.Fatalf("did not expect neg-margin relaxation when local fee is near the seed ceiling")
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

func TestComputeInboundDiscountUsesAppliedOutboundCap(t *testing.T) {
	got := computeInboundDiscount(true, "sink", 0.05, 6, 300, 10, 1000, 0.95, 0.10, 0.01)
	if got != 950 {
		t.Fatalf("expected inbound discount capped by applied outbound fee ratio: got %d want 950", got)
	}
}

func TestComputeInboundDiscountRejectsIneligibleChannel(t *testing.T) {
	got := computeInboundDiscount(true, "router", 0.05, 6, 300, 100, 1000, 0.90, 0.10, 0.12)
	if got != 0 {
		t.Fatalf("expected no inbound discount for non-sink channel, got %d", got)
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
	profile := autofeeProfiles["moderate"]
	if got := reversalConfirmRoundsForChannel(profile, nil, 80.0); got != profile.ReversalConfirmMinRounds {
		t.Fatalf("unexpected rounds for nil state: got %d want %d", got, reversalConfirmMinRounds)
	}

	st := &autofeeChannelState{StalledRounds: profile.ReversalFastTrackStallRounds - 1}
	if got := reversalConfirmRoundsForChannel(profile, st, 80.0); got != profile.ReversalConfirmMinRounds {
		t.Fatalf("unexpected rounds below stall threshold: got %d want %d", got, profile.ReversalConfirmMinRounds)
	}

	st.StalledRounds = profile.ReversalFastTrackStallRounds
	if got := reversalConfirmRoundsForChannel(profile, st, profile.ReversalFastTrackGapFrac*100.0-0.1); got != profile.ReversalConfirmMinRounds {
		t.Fatalf("unexpected rounds below gap threshold: got %d want %d", got, profile.ReversalConfirmMinRounds)
	}
	if got := reversalConfirmRoundsForChannel(profile, st, profile.ReversalFastTrackGapFrac*100.0); got != profile.ReversalConfirmMinRounds-1 {
		t.Fatalf("unexpected rounds at fast-track threshold: got %d want %d", got, profile.ReversalConfirmMinRounds-1)
	}
}

func TestAntiFlipExtraConfirmRoundsForChannel(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	now := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
	st := &autofeeChannelState{
		LastDir: "up",
		ExplorerState: explorerState{
			LastReversalDir: "up",
			LastReversalTs:  now.Add(-2 * time.Hour).Unix(),
		},
	}

	extra, tags := antiFlipExtraConfirmRoundsForChannel(profile, st, now, 1000, 930, 22.0, []string{"trend-down", "peg"})
	if extra != profile.AntiFlipExtraConfirmRounds {
		t.Fatalf("unexpected anti-flip extra rounds: got %d want %d", extra, profile.AntiFlipExtraConfirmRounds)
	}
	if len(tags) == 0 || tags[0] != "anti-flip-window" {
		t.Fatalf("expected anti-flip tag, got %+v", tags)
	}
}

func TestAntiFlipExtraConfirmRoundsBypassesStrongSignal(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	now := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
	st := &autofeeChannelState{
		LastDir: "up",
		ExplorerState: explorerState{
			LastReversalDir: "up",
			LastReversalTs:  now.Add(-2 * time.Hour).Unix(),
		},
	}

	extra, tags := antiFlipExtraConfirmRoundsForChannel(profile, st, now, 1000, 930, profile.AntiFlipStrongGapFrac*100.0+5.0, []string{"trend-down"})
	if extra != 0 || len(tags) != 0 {
		t.Fatalf("expected strong gap to bypass anti-flip guard, got rounds=%d tags=%+v", extra, tags)
	}

	extra, tags = antiFlipExtraConfirmRoundsForChannel(profile, st, now, 1000, 930, 20.0, []string{"htlc-liquidity-hot"})
	if extra != 0 || len(tags) != 0 {
		t.Fatalf("expected strong signal tag to bypass anti-flip guard, got rounds=%d tags=%+v", extra, tags)
	}
}

func TestCapBalancedFloorDrivenUpForRouter(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	capped, tags := capBalancedFloorDrivenUp(profile, "router", 0.32, 1710, 1533, 2012)
	if capped >= 2012 {
		t.Fatalf("expected balanced floor-driven rise to be capped below floor, got %d", capped)
	}
	if capped != 1795 {
		t.Fatalf("unexpected capped ppm: got %d want 1795", capped)
	}
	if len(tags) == 0 || tags[0] != "balanced-floor-up-cap" {
		t.Fatalf("expected balanced floor cap tag, got %+v", tags)
	}
}

func TestCapBalancedFloorDrivenUpBypassesDrainedSink(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	capped, tags := capBalancedFloorDrivenUp(profile, "sink", 0.08, 1000, 950, 1180)
	if capped != 1180 {
		t.Fatalf("expected drained sink to keep stronger upward floor, got %d", capped)
	}
	if len(tags) != 0 {
		t.Fatalf("did not expect cap tag for drained sink, got %+v", tags)
	}
}

func TestCapBalancedFloorDrivenUpForMidLiquiditySink(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	capped, tags := capBalancedFloorDrivenUp(profile, "sink", 0.24, 1784, 1592, 2369)
	if capped >= 2369 {
		t.Fatalf("expected mid-liquidity sink rise to be capped below floor, got %d", capped)
	}
	if capped != 1873 {
		t.Fatalf("unexpected capped ppm for mid-liquidity sink: got %d want 1873", capped)
	}
	if len(tags) == 0 || tags[0] != "balanced-floor-up-cap" {
		t.Fatalf("expected balanced floor cap tag for mid-liquidity sink, got %+v", tags)
	}
}

func TestApplyOutrateTargetAnchorModerate(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	anchored, tags := applyOutrateTargetAnchor(profile, 512, 1086, 0.32, 9)
	if anchored != 869 {
		t.Fatalf("unexpected anchored target: got %d want 869", anchored)
	}
	if len(tags) != 1 || tags[0] != "outrate-target-anchor" {
		t.Fatalf("unexpected outrate anchor tags: %+v", tags)
	}
}

func TestApplyOutrateTargetAnchorBypassesLowLiquidity(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	anchored, tags := applyOutrateTargetAnchor(profile, 512, 1086, 0.12, 9)
	if anchored != 512 {
		t.Fatalf("expected low-liquidity channel to keep original target, got %d", anchored)
	}
	if len(tags) != 0 {
		t.Fatalf("did not expect outrate anchor tags, got %+v", tags)
	}
}

func TestShouldApplyFailedRebalancePressure(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	if !shouldApplyFailedRebalancePressure(profile, 0.08, profile.LowOutProtectThresh, 0, profile.RebalFailNoDownMinAttempts) {
		t.Fatalf("expected failed rebalance pressure on drained channel without recent success")
	}
	if shouldApplyFailedRebalancePressure(profile, 0.25, profile.LowOutProtectThresh, 0, profile.RebalFailNoDownMinAttempts) {
		t.Fatalf("did not expect failed rebalance pressure on balanced channel")
	}
	if shouldApplyFailedRebalancePressure(profile, 0.08, profile.LowOutProtectThresh, 1, profile.RebalFailNoDownMinAttempts+2) {
		t.Fatalf("did not expect failed rebalance pressure when recent success exists")
	}
}

func TestApplyFailedRebalancePressure(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	target, tags := applyFailedRebalancePressure(profile, 1000, 940, profile.RebalFailUpMinAttempts, true, false)
	if target <= 1000 {
		t.Fatalf("expected failed rebalance pressure to lift target above current ppm, got %d", target)
	}
	if len(tags) < 2 || tags[0] != "rebal-fail-nodown" || tags[1] != "rebal-fail-pressure" {
		t.Fatalf("unexpected rebalance failure tags: %+v", tags)
	}

	target, tags = applyFailedRebalancePressure(profile, 1000, 980, profile.RebalFailNoDownMinAttempts, false, false)
	if target != 1000 {
		t.Fatalf("expected no-down protection without strong pressure signal, got %d", target)
	}
	if len(tags) != 1 || tags[0] != "rebal-fail-nodown" {
		t.Fatalf("unexpected rebalance no-down tags: %+v", tags)
	}
}

func TestRebalanceFailureSignalWindow(t *testing.T) {
	e := &autofeeEngine{
		cfg:     AutofeeConfig{RunIntervalSec: 3600},
		profile: autofeeProfiles["moderate"],
	}
	if got := e.rebalanceFailureSignalWindow(); got != 6*time.Hour {
		t.Fatalf("unexpected moderate rebalance failure window: got %s want 6h", got)
	}

	e = &autofeeEngine{
		cfg:     AutofeeConfig{RunIntervalSec: 4 * 3600},
		profile: autofeeProfiles["aggressive"],
	}
	if got := e.rebalanceFailureSignalWindow(); got != 4*time.Hour {
		t.Fatalf("expected failure window not shorter than success window: got %s want 4h", got)
	}
}

func TestRebalanceFailureCampaignGap(t *testing.T) {
	e := &autofeeEngine{
		cfg:     AutofeeConfig{RunIntervalSec: 3600},
		profile: autofeeProfiles["moderate"],
	}
	if got := e.rebalanceFailureCampaignGap(); got != 90*time.Minute {
		t.Fatalf("unexpected moderate rebalance campaign gap: got %s want 90m", got)
	}

	e = &autofeeEngine{
		cfg:     AutofeeConfig{RunIntervalSec: 3600},
		profile: autofeeProfiles["aggressive"],
	}
	if got := e.rebalanceFailureCampaignGap(); got != 60*time.Minute {
		t.Fatalf("unexpected aggressive rebalance campaign gap: got %s want 60m", got)
	}
}

func TestEffectiveCooldownUpSecForChannel(t *testing.T) {
	profile := autofeeProfiles["moderate"]

	if got := effectiveCooldownUpSecForChannel(profile, profile.CooldownUpSec, 0.20, false); got != 6*3600 {
		t.Fatalf("unexpected normal moderate cooldown: got %d want %d", got, 6*3600)
	}
	if got := effectiveCooldownUpSecForChannel(profile, profile.CooldownUpSec, 0.04, false); got != 3*3600 {
		t.Fatalf("unexpected drained moderate cooldown: got %d want %d", got, 3*3600)
	}
	if got := effectiveCooldownUpSecForChannel(profile, profile.CooldownUpSec, 0.009, false); got != 3600 {
		t.Fatalf("unexpected extreme moderate cooldown: got %d want %d", got, 3600)
	}
}

func TestEffectiveCooldownUpSecForChannelRespectsHoldAndBase(t *testing.T) {
	profile := autofeeProfiles["aggressive"]

	if got := effectiveCooldownUpSecForChannel(profile, profile.CooldownUpSec, 0.009, true); got != profile.CooldownUpSec {
		t.Fatalf("expected recent rebalance hold to keep base cooldown, got %d want %d", got, profile.CooldownUpSec)
	}

	if got := effectiveCooldownUpSecForChannel(profile, 1800, 0.009, false); got != 3600 {
		t.Fatalf("expected minimum cooldown floor to apply, got %d want %d", got, 3600)
	}
}

func TestEvalDrainedExplorerStartsAndStops(t *testing.T) {
	e := &autofeeEngine{
		profile: autofeeProfiles["moderate"],
		now:     time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC),
	}
	st := &autofeeChannelState{LastTs: e.now.Add(-8 * 24 * time.Hour)}

	if !e.evalDrainedExplorer(st, 0.03, 0, 0) {
		t.Fatalf("expected drained explorer to start on drained inactive channel")
	}
	if !st.ExplorerState.DrainedActive || !st.ExplorerState.DrainedSeen {
		t.Fatalf("expected drained explorer state to be active: %+v", st.ExplorerState)
	}

	st.ExplorerState.DrainedRounds = e.profile.DrainedExplorerMaxRounds
	if e.evalDrainedExplorer(st, 0.03, 0, 0) {
		t.Fatalf("expected drained explorer to stop after max rounds")
	}
	if st.ExplorerState.DrainedActive {
		t.Fatalf("expected drained explorer state to be cleared")
	}
}

func TestApplyDrainedExplorerTargetAndCap(t *testing.T) {
	profile := autofeeProfiles["moderate"]

	target, tags := applyDrainedExplorerTarget(profile, true, 0, 0, 0, 113, 113)
	if target != 10 {
		t.Fatalf("unexpected drained explorer target: got %d want 10", target)
	}
	if len(tags) < 2 || tags[0] != "drained-explorer" || tags[1] != "drained-explorer-r1" {
		t.Fatalf("unexpected drained explorer tags: %+v", tags)
	}

	capped, capTags := capWeakDemandFloorUpForDrainedExplorer(profile, true, 0, 125)
	if capped != 10 {
		t.Fatalf("expected weak-demand floor up to be capped to explorer step, got %d want 10", capped)
	}
	if len(capTags) != 1 || capTags[0] != "drained-explorer-cap" {
		t.Fatalf("unexpected drained explorer cap tags: %+v", capTags)
	}
}

func TestCollapseWeakRebalanceCampaigns(t *testing.T) {
	base := time.Date(2026, 3, 21, 12, 0, 0, 0, time.UTC)
	jobs := []recentWeakRebalanceJob{
		{ChannelID: 1, Ts: base.Add(-80 * time.Minute), AmtSat: 100000},
		{ChannelID: 1, Ts: base.Add(-50 * time.Minute), AmtSat: 120000},
		{ChannelID: 1, Ts: base.Add(-1 * time.Minute), AmtSat: 90000},
		{ChannelID: 2, Ts: base.Add(-15 * time.Minute), AmtSat: 50000},
	}
	got := collapseWeakRebalanceCampaigns(jobs, 45*time.Minute)
	if got[1].WeakCount != 2 {
		t.Fatalf("unexpected campaign count for chan 1: got %d want 2", got[1].WeakCount)
	}
	if got[1].WeakAmtSat != 210000 {
		t.Fatalf("unexpected campaign amt for chan 1: got %d want 210000", got[1].WeakAmtSat)
	}
	if got[2].WeakCount != 1 || got[2].WeakAmtSat != 50000 {
		t.Fatalf("unexpected campaign aggregation for chan 2: %+v", got[2])
	}
}

func TestAutofeeProfileMovementDefaults(t *testing.T) {
	conservative := autofeeProfiles["conservative"]
	if conservative.StepCap != 0.05 || conservative.CooldownUpSec != 8*3600 || conservative.CooldownDownSec != 2*3600 {
		t.Fatalf("unexpected conservative movement defaults: %+v", conservative)
	}
	if conservative.CooldownUpDrainedSec != 4*3600 || conservative.CooldownUpExtremeSec != 2*3600 {
		t.Fatalf("unexpected conservative drained cooldown defaults: %+v", conservative)
	}
	if conservative.DiscoveryStepCapDown != 0.15 || conservative.StallFloorRelaxGapFrac != 0.15 {
		t.Fatalf("unexpected conservative down movement tuning: %+v", conservative)
	}
	if conservative.SoftenMinOutRatio != 0.25 || conservative.SoftenMaxDropToPegFrac != 0.85 {
		t.Fatalf("unexpected conservative soften tuning: %+v", conservative)
	}

	moderate := autofeeProfiles["moderate"]
	if moderate.StepCap != 0.08 || moderate.CooldownUpSec != 6*3600 || moderate.CooldownDownSec != 1*3600 {
		t.Fatalf("unexpected moderate movement defaults: %+v", moderate)
	}
	if moderate.CooldownUpDrainedSec != 3*3600 || moderate.CooldownUpExtremeSec != 1*3600 {
		t.Fatalf("unexpected moderate drained cooldown defaults: %+v", moderate)
	}
	if moderate.DiscoveryStepCapDown != 0.20 || moderate.StallFloorRelaxGapFrac != 0.10 {
		t.Fatalf("unexpected moderate down movement tuning: %+v", moderate)
	}
	if moderate.SoftenMinOutRatio != 0.20 || moderate.SoftenMaxDropToPegFrac != 0.75 {
		t.Fatalf("unexpected moderate soften tuning: %+v", moderate)
	}

	aggressive := autofeeProfiles["aggressive"]
	if aggressive.StepCap != 0.10 || aggressive.CooldownUpSec != 3*3600 || aggressive.CooldownDownSec != 1*3600 {
		t.Fatalf("unexpected aggressive movement defaults: %+v", aggressive)
	}
	if aggressive.CooldownUpDrainedSec != 90*60 || aggressive.CooldownUpExtremeSec != 3600 {
		t.Fatalf("unexpected aggressive drained cooldown defaults: %+v", aggressive)
	}
	if aggressive.DiscoveryStepCapDown != 0.25 || aggressive.StallFloorRelaxGapFrac != 0.08 {
		t.Fatalf("unexpected aggressive down movement tuning: %+v", aggressive)
	}
	if aggressive.SoftenMinOutRatio != 0.15 || aggressive.SoftenMaxDropToPegFrac != 0.70 {
		t.Fatalf("unexpected aggressive soften tuning: %+v", aggressive)
	}
}

func TestAutofeeConfigWithProfileDefaults(t *testing.T) {
	cfg := autofeeConfigWithProfileDefaults(AutofeeConfig{Profile: "moderate"})
	if len(cfg.ProfileDefaults) != len(autofeeProfiles) {
		t.Fatalf("unexpected profile defaults count: got %d want %d", len(cfg.ProfileDefaults), len(autofeeProfiles))
	}
	moderate, ok := cfg.ProfileDefaults["moderate"]
	if !ok {
		t.Fatalf("expected moderate profile defaults to be present")
	}
	if moderate.CooldownUpSec != 6*3600 || moderate.CooldownDownSec != 1*3600 || moderate.StepCap != 0.08 {
		t.Fatalf("unexpected moderate profile defaults payload: %+v", moderate)
	}
	if moderate.InboundDiscountReachOutRatio != 0.15 || moderate.InboundDiscountMinRetainedSpreadFrac != 0.12 {
		t.Fatalf("unexpected inbound discount defaults payload: %+v", moderate)
	}
	if moderate.InboundDiscountMaxRatio != defaultInboundDiscountMaxRatio {
		t.Fatalf("unexpected inbound discount default: got %.2f want %.2f", moderate.InboundDiscountMaxRatio, defaultInboundDiscountMaxRatio)
	}
}

func TestComputeMarketRefillInboundDiscount(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	discount, tags := computeMarketRefillInboundDiscount(true, 0.08, 3, false, false, 0, 200, 1000, 0.95, 0.10, profile)
	if discount <= 0 {
		t.Fatalf("expected strategic market refill inbound discount")
	}
	if len(tags) == 0 || tags[0] != "market-refill-inbound" {
		t.Fatalf("unexpected strategic tags: %+v", tags)
	}

	exploratoryDiscount, exploratoryTags := computeMarketRefillInboundDiscount(true, 0.22, 0, true, true, 0, 200, 1000, 0.95, 0.10, profile)
	if exploratoryDiscount <= 0 {
		t.Fatalf("expected exploratory market refill inbound discount")
	}
	if len(exploratoryTags) < 2 || exploratoryTags[1] != "market-refill-explore" {
		t.Fatalf("unexpected exploratory tags: %+v", exploratoryTags)
	}

	fullSideDiscount, fullSideTags := computeMarketRefillInboundDiscount(true, 0.78, 0, false, false, 0, 200, 1000, 0.95, 0.10, profile)
	if fullSideDiscount <= 0 {
		t.Fatalf("expected market refill mode to price full-side channels too")
	}
	if len(fullSideTags) == 0 || fullSideTags[0] != "market-refill-inbound" {
		t.Fatalf("unexpected full-side tags: %+v", fullSideTags)
	}
}

func TestComputeMarketRefillInboundDiscountAggressiveFullExploreStaysCloseToOutbound(t *testing.T) {
	profile := autofeeProfiles["aggressive"]

	discount, tags := computeMarketRefillInboundDiscount(true, 0.78, 0, true, false, 0, 110, 2490, 0.95, 0.08, profile)
	if discount != 2179 {
		t.Fatalf("expected aggressive full/explore inbound discount to stay close to outbound while respecting cost/spread guards, got %d want 2179", discount)
	}
	if len(tags) < 2 || tags[0] != "market-refill-inbound" || tags[1] != "market-refill-explore" {
		t.Fatalf("unexpected aggressive full/explore tags: %+v", tags)
	}
}

func TestComputeMarketRefillInboundDiscountUsesEffectiveOutRatio(t *testing.T) {
	profile := autofeeProfiles["moderate"]

	drainedDiscount, _ := computeMarketRefillInboundDiscount(true, 0.08, 1, false, false, 0, 50, 1000, 0.95, 0.01, profile)
	fullerDiscount, _ := computeMarketRefillInboundDiscount(true, 0.28, 1, false, false, 0, 50, 1000, 0.95, 0.01, profile)

	if drainedDiscount <= 0 || fullerDiscount <= 0 {
		t.Fatalf("expected positive inbound discount in both scenarios, got drained=%d fuller=%d", drainedDiscount, fullerDiscount)
	}
	if drainedDiscount <= fullerDiscount {
		t.Fatalf("expected more aggressive inbound discount for lower effective out ratio, got drained=%d fuller=%d", drainedDiscount, fullerDiscount)
	}
}

func TestAdjustMarketRefillInboundTargetFracByPeerSkew(t *testing.T) {
	baseFrac := 0.20

	adjusted, tags := adjustMarketRefillInboundTargetFracByPeerSkew(baseFrac, 15.0)
	if adjusted >= baseFrac {
		t.Fatalf("expected peer skew to reduce net inbound target fraction, got base=%.3f adjusted=%.3f", baseFrac, adjusted)
	}
	if len(tags) < 2 || tags[0] != "market-refill-skew" || tags[1] != "market-refill-skew-med" {
		t.Fatalf("unexpected skew tags: %+v", tags)
	}
}

func TestApplyMarketRefillOutboundBias(t *testing.T) {
	profile := autofeeProfiles["moderate"]

	target, tags := applyMarketRefillOutboundBias(profile, 100, 105, 150, 0.08, 2, false, false, 0, 3500)
	if target != 705 {
		t.Fatalf("unexpected drained market refill target uplift: got %d want 705", target)
	}
	if len(tags) < 2 || tags[0] != "market-refill-up" || tags[1] != "market-refill-drained" {
		t.Fatalf("unexpected strategic outbound tags: %+v", tags)
	}

	exploreTarget, exploreTags := applyMarketRefillOutboundBias(profile, 100, 102, 0, 0.78, 0, true, true, 0, 3500)
	if exploreTarget != 202 {
		t.Fatalf("unexpected exploratory market refill target uplift: got %d want 202", exploreTarget)
	}
	if len(exploreTags) < 3 || exploreTags[1] != "market-refill-node" || exploreTags[2] != "market-refill-explore" {
		t.Fatalf("unexpected exploratory outbound tags: %+v", exploreTags)
	}

	outrateTarget, outrateTags := applyMarketRefillOutboundBias(profile, 100, 220, 1200, 0.40, 4, false, false, 0, 3500)
	if outrateTarget != 1200 {
		t.Fatalf("expected market refill outrate floor to win when stronger than premium, got %d want 1200", outrateTarget)
	}
	if len(outrateTags) < 2 || outrateTags[1] != "market-refill-node" {
		t.Fatalf("unexpected market refill outrate tags: %+v", outrateTags)
	}
}

func TestApplyMarketRefillOutboundBiasUsesSoftBootstrapFloor(t *testing.T) {
	profile := autofeeProfiles["aggressive"]

	target, _ := applyMarketRefillOutboundBias(profile, 100, 200, 0, 0.70, 0, true, true, 0, 2000)
	if target != 350 {
		t.Fatalf("expected aggressive market refill to stay dynamic at low target values, got %d want 350", target)
	}

	target, _ = applyMarketRefillOutboundBias(profile, 100, 200, 0, 0.70, 0, true, true, 0, 4000)
	if target != 350 {
		t.Fatalf("expected aggressive market refill to avoid collapsing to a fixed max-ppm floor, got %d want 350", target)
	}
}

func TestApplyMarketRefillOutboundBiasCanReachMaxPpmFromHighBalancedTarget(t *testing.T) {
	profile := autofeeProfiles["aggressive"]

	target, tags := applyMarketRefillOutboundBias(profile, 200, 1800, 0, 0.08, 0, true, true, 0, 4000)
	if target != 4000 {
		t.Fatalf("expected aggressive market refill to climb to max_ppm from a high balanced target, got %d want 4000", target)
	}
	if len(tags) < 3 || tags[0] != "market-refill-up" || tags[1] != "market-refill-drained" || tags[2] != "market-refill-explore" {
		t.Fatalf("unexpected dynamic max-ppm tags: %+v", tags)
	}
}

func TestApplyMarketRefillOutboundBiasCapsLocalMemoryAgainstBalancedTarget(t *testing.T) {
	profile := autofeeProfiles["aggressive"]

	target, tags := applyMarketRefillOutboundBias(profile, 2500, 300, 0, 0.70, 0, true, true, 0, 4000)
	if target != 630 {
		t.Fatalf("expected local memory to be capped by balanced target context, got %d want 630", target)
	}
	if len(tags) < 3 || tags[0] != "market-refill-up" || tags[1] != "market-refill-node" || tags[2] != "market-refill-explore" {
		t.Fatalf("unexpected capped-local tags: %+v", tags)
	}
}

func TestApplySeedSignalCapsModerate(t *testing.T) {
	profile := autofeeProfiles["moderate"]

	seed, tags := applySeedSignalCaps(profile, 3482, 1782, 1638, 1868)
	if math.Abs(seed-2138.4) > 0.001 {
		t.Fatalf("expected moderate seed cap to stay close to outrate while considering rebal floor, got %.1f want 2138.4", seed)
	}
	if len(tags) < 3 || tags[0] != "seed:rebalfloor" || tags[1] != "seed:outcap" || tags[2] != "seed:rebalcap" {
		t.Fatalf("unexpected seed cap tags: %+v", tags)
	}
}

func TestApplySeedSignalCapsAggressive(t *testing.T) {
	profile := autofeeProfiles["aggressive"]

	seed, _ := applySeedSignalCaps(profile, 3482, 1782, 1638, 1868)
	if math.Abs(seed-2241.6) > 0.001 {
		t.Fatalf("expected aggressive seed cap to consider rebal floor, got %.1f want 2241.6", seed)
	}
}

func TestManageRescueStateEnterAndExit(t *testing.T) {
	st := &autofeeChannelState{}
	now := time.Unix(1_700_000_000, 0).UTC()
	ranking := autofeeRankingSnapshot{
		Score:          19,
		State:          "close",
		TrendDirection: "worsening",
		ProfitFee7dSat: -229,
	}

	active, tags := manageRescueState(st, now, true, ranking, true, 1008, 637, 1202, 0.003, false)
	if !active {
		t.Fatalf("expected rescue to activate on weak close-state channel")
	}
	if !containsTag(tags, "rescue-enter") || !st.ExplorerState.RescueActive || st.ExplorerState.RescueRounds != 1 {
		t.Fatalf("unexpected rescue entry state: tags=%+v state=%+v", tags, st.ExplorerState)
	}

	st.ExplorerState.RescueRounds = rescueMinRounds
	st.ExplorerState.RescueRecoverRounds = 0
	recovered := autofeeRankingSnapshot{
		Score:          72,
		State:          "expand",
		TrendDirection: "stable",
		ProfitFee7dSat: 1500,
	}
	active, tags = manageRescueState(st, now.Add(13*time.Hour), true, recovered, true, 740, 700, 700, 0.001, false)
	if !active || st.ExplorerState.RescueRecoverRounds != 1 {
		t.Fatalf("expected rescue exit to require confirmation, active=%v state=%+v tags=%+v", active, st.ExplorerState, tags)
	}
	active, tags = manageRescueState(st, now.Add(14*time.Hour), true, recovered, true, 740, 700, 700, 0.001, false)
	if active {
		t.Fatalf("expected rescue to exit after recovery")
	}
	if !containsTag(tags, "rescue-exit") || st.ExplorerState.RescueActive {
		t.Fatalf("unexpected rescue exit state: tags=%+v state=%+v", tags, st.ExplorerState)
	}
}

func TestManageRescueStatePriorityBypassesReentryCooldown(t *testing.T) {
	st := &autofeeChannelState{}
	now := time.Unix(1_700_000_000, 0).UTC()
	st.ExplorerState.RescueLastExitTs = now.Add(-2 * time.Hour).Unix()
	ranking := autofeeRankingSnapshot{
		Score:          14,
		State:          "close",
		TrendDirection: "worsening",
		ProfitFee7dSat: -1052,
	}

	active, tags := manageRescueState(st, now, true, ranking, true, 1263, 640, 1202, 0.003, false)
	if !active {
		t.Fatalf("expected high-priority rescue candidate to bypass reentry cooldown")
	}
	if !containsTag(tags, "rescue-enter") || !st.ExplorerState.RescueActive {
		t.Fatalf("unexpected priority rescue entry state: tags=%+v state=%+v", tags, st.ExplorerState)
	}
}

func TestApplyRescueFloorRelax(t *testing.T) {
	floor, src, tags := applyRescueFloorRelax(true, 1263, 640, 1263, "peg", 1202, 869)
	if floor != 1202 {
		t.Fatalf("expected rescue floor relax to move peg floor closer to outrate, got %d want 1202", floor)
	}
	if src != "rescue" {
		t.Fatalf("expected rescue floor source, got %q", src)
	}
	if !containsTag(tags, "rescue-floor-relax") {
		t.Fatalf("expected rescue floor relax tag, got %+v", tags)
	}
}

func TestMarketRefillStepCapFrac(t *testing.T) {
	profile := autofeeProfiles["moderate"]

	if got, tags := marketRefillStepCapFrac(profile, 0.08); got != 0.40 || len(tags) < 2 || tags[1] != "market-refill-stepcap-drained" {
		t.Fatalf("unexpected drained market refill cap: got %.2f tags=%+v", got, tags)
	}
	if got, tags := marketRefillStepCapFrac(profile, 0.22); got != 0.30 || len(tags) < 2 || tags[1] != "market-refill-stepcap-low" {
		t.Fatalf("unexpected low market refill cap: got %.2f tags=%+v", got, tags)
	}
	if got, tags := marketRefillStepCapFrac(profile, 0.70); got != 0.20 || len(tags) < 1 || tags[0] != "market-refill-stepcap" {
		t.Fatalf("unexpected node-wide market refill cap: got %.2f tags=%+v", got, tags)
	}
}

func TestShouldBypassMarketRefillReversalGuard(t *testing.T) {
	if !shouldBypassMarketRefillReversalGuard([]string{"market-refill-up"}, 120, 900, 500) {
		t.Fatalf("expected market refill reversal guard bypass on large upward regime switch")
	}
	if shouldBypassMarketRefillReversalGuard([]string{"market-refill-up"}, 20, 900, 500) {
		t.Fatalf("did not expect bypass on small target gap")
	}
	if shouldBypassMarketRefillReversalGuard([]string{"trend-up"}, 120, 900, 500) {
		t.Fatalf("did not expect bypass without market refill uplift tag")
	}
}

func TestShouldHoldSmallStepBypassesTowardTargetWhenGapIsLarge(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	st := &autofeeChannelState{}
	if shouldHoldSmallStep(profile, st, 1000, 988, 700, false) {
		t.Fatalf("expected small downward step toward a distant target to be applied")
	}
}

func TestShouldHoldSmallStepKeepsNoiseWhenGapIsSmall(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	st := &autofeeChannelState{}
	if !shouldHoldSmallStep(profile, st, 1000, 988, 970, false) {
		t.Fatalf("expected small move near target to be held")
	}
}

func TestShouldHoldSmallStepBypassesWhenChannelIsStalled(t *testing.T) {
	profile := autofeeProfiles["conservative"]
	st := &autofeeChannelState{StalledRounds: profile.HoldSmallStallBypassRounds}
	if shouldHoldSmallStep(profile, st, 1000, 988, 930, false) {
		t.Fatalf("expected stalled channel to bypass hold-small when moving toward target")
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
