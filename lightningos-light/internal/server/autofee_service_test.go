package server

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/lndclient"
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

func TestShouldHoldMatureEmptySinkUpwardPressure(t *testing.T) {
	if !shouldHoldMatureEmptySinkUpwardPressure("sink", 24*10, 48, 0.05, 0.10, 650, 780, 0, 0, 0, false, false) {
		t.Fatalf("expected mature empty sink with history and no signals to hold upward pressure")
	}
	if shouldHoldMatureEmptySinkUpwardPressure("sink", 24*2, 48, 0.05, 0.10, 650, 780, 0, 0, 0, false, false) {
		t.Fatalf("did not expect hold during channel warmup")
	}
	if shouldHoldMatureEmptySinkUpwardPressure("sink", 24*10, 48, 0.05, 0.10, 650, 780, 1, 0, 0, false, false) {
		t.Fatalf("did not expect hold when recent forwards exist")
	}
	if shouldHoldMatureEmptySinkUpwardPressure("sink", 24*10, 48, 0.05, 0.10, 650, 780, 0, 1, 0, false, false) {
		t.Fatalf("did not expect hold when recent rebalance succeeded")
	}
	if shouldHoldMatureEmptySinkUpwardPressure("sink", 24*10, 48, 0.05, 0.10, 650, 780, 0, 0, 1, false, false) {
		t.Fatalf("did not expect hold when recent weak rebalance attempts exist")
	}
	if shouldHoldMatureEmptySinkUpwardPressure("sink", 24*10, 48, 0.05, 0.10, 650, 780, 0, 0, 0, true, false) {
		t.Fatalf("did not expect hold with HTLC pressure signal")
	}
	if shouldHoldMatureEmptySinkUpwardPressure("sink", 24*10, 48, 0.05, 0.10, 650, 780, 0, 0, 0, false, true) {
		t.Fatalf("did not expect hold with HTLC forward hot signal")
	}
}

func TestDeriveMatureEmptySinkHistoryAnchor(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	anchor, ok := deriveMatureEmptySinkHistoryAnchor(profile, 2207, 748, 971, 971, "rebal")
	if !ok {
		t.Fatalf("expected mature empty sink anchor to activate")
	}
	if anchor != 1185 {
		t.Fatalf("unexpected anchor: got %d want 1185", anchor)
	}

	if _, ok := deriveMatureEmptySinkHistoryAnchor(profile, 1200, 748, 971, 971, "rebal"); ok {
		t.Fatalf("did not expect anchor when local ppm is not detached enough")
	}
}

func TestShouldRelaxFloorForMatureEmptySinkAnchor(t *testing.T) {
	if !shouldRelaxFloorForMatureEmptySinkAnchor("rebal") || !shouldRelaxFloorForMatureEmptySinkAnchor("peg") || !shouldRelaxFloorForMatureEmptySinkAnchor("outrate-sink") {
		t.Fatalf("expected history-derived floor sources to be relaxable")
	}
	if shouldRelaxFloorForMatureEmptySinkAnchor("revfloor") || shouldRelaxFloorForMatureEmptySinkAnchor("super-source") {
		t.Fatalf("did not expect revfloor or super-source to be relaxable")
	}
}

func TestHasFreshAutofeePressureSignal(t *testing.T) {
	if !hasFreshAutofeePressureSignal(0, 0, 1, 0, false, false, false) {
		t.Fatalf("expected recent rebalance success to count as fresh pressure")
	}
	if !hasFreshAutofeePressureSignal(0, 0, 0, 1, false, false, false) {
		t.Fatalf("expected recent weak rebalance attempt to count as fresh pressure")
	}
	if !hasFreshAutofeePressureSignal(1, 0, 0, 0, false, false, false) {
		t.Fatalf("expected recent forwards to count as fresh pressure")
	}
	if !hasFreshAutofeePressureSignal(0, 0, 0, 0, true, false, false) {
		t.Fatalf("expected HTLC pressure to count as fresh pressure")
	}
	if !hasFreshAutofeePressureSignal(0, 0, 0, 0, false, false, true) {
		t.Fatalf("expected bootstrap state to count as fresh pressure")
	}
	if hasFreshAutofeePressureSignal(0, 0, 0, 0, false, false, false) {
		t.Fatalf("did not expect idle channel to count as fresh pressure")
	}
}

func TestShouldBlockAutofeeIdleUpwardPressure(t *testing.T) {
	if !shouldBlockAutofeeIdleUpwardPressure(false, true, 250, 200, 0, 0, false, false, false, false, false) {
		t.Fatalf("expected idle upward pressure to be blocked without observed signals")
	}
	if shouldBlockAutofeeIdleUpwardPressure(false, true, 250, 200, 0, 0, false, false, false, true, false) {
		t.Fatalf("did not expect upward pressure to be blocked when observed forward signal exists")
	}
	if shouldBlockAutofeeIdleUpwardPressure(false, true, 250, 200, 0, 1, false, false, false, false, false) {
		t.Fatalf("did not expect upward pressure to be blocked when weak rebalance attempts exist")
	}
	if shouldBlockAutofeeIdleUpwardPressure(false, true, 250, 200, 0, 0, false, false, true, false, false) {
		t.Fatalf("did not expect upward pressure to be blocked during bootstrap")
	}
	if shouldBlockAutofeeIdleUpwardPressure(true, true, 250, 200, 0, 0, false, false, false, false, false) {
		t.Fatalf("did not expect balanced-mode idle rule to apply in market refill mode")
	}
}

func TestShouldRefreshAutofeeOutrateMemory(t *testing.T) {
	if !shouldRefreshAutofeeOutrateMemory(400, 1, 1000) {
		t.Fatalf("expected recent 1d forwards to refresh outrate memory")
	}
	if shouldRefreshAutofeeOutrateMemory(400, 0, 0) {
		t.Fatalf("did not expect stale 7d-only outrate to refresh memory timestamp")
	}
	if shouldRefreshAutofeeOutrateMemory(0, 1, 1000) {
		t.Fatalf("did not expect zero outrate to refresh memory timestamp")
	}
}

func TestBaseCostSourceClassification(t *testing.T) {
	if !isRebalanceBaseCostSource("rebal") || !isRebalanceBaseCostSource("rebal-global") || !isRebalanceBaseCostSource("rebal-blend") || !isRebalanceBaseCostSource("rebal-recent") {
		t.Fatalf("expected rebalance-derived sources to be classified as rebalance")
	}
	if isRebalanceBaseCostSource("outrate") || isRebalanceBaseCostSource("seed") {
		t.Fatalf("did not expect outrate/seed sources to be classified as rebalance")
	}
	if !isOutrateBaseCostSource("outrate") || !isOutrateBaseCostSource("outrate-mem") {
		t.Fatalf("expected outrate-derived sources to be classified as outrate")
	}
	if isOutrateBaseCostSource("rebal") {
		t.Fatalf("did not expect rebalance source to be classified as outrate")
	}
}

func TestFloorSourceFromBaseCost(t *testing.T) {
	if got := floorSourceFromBaseCost("outrate", false); got != "outrate" {
		t.Fatalf("unexpected floor source for outrate fallback: got %q", got)
	}
	if got := floorSourceFromBaseCost("seed", false); got != "seed" {
		t.Fatalf("unexpected floor source for seed fallback: got %q", got)
	}
	if got := floorSourceFromBaseCost("rebal", false); got != "rebal" {
		t.Fatalf("unexpected floor source for rebalance fallback: got %q", got)
	}
	if got := floorSourceFromBaseCost("min", false); got != "min" {
		t.Fatalf("unexpected floor source for minimum fallback: got %q", got)
	}
	if got := floorSourceFromBaseCost("outrate", true); got != "market" {
		t.Fatalf("unexpected floor source for market refill mode: got %q", got)
	}
}

func TestShouldSoftenHistoricalRebalanceFloorForGoodLiquidity(t *testing.T) {
	if !shouldSoftenHistoricalRebalanceFloor(false, "rebal", "rebal", 444, 444, 1166, 371, true, true, 0, 0, []string{"htlc-forward-hot"}) {
		t.Fatalf("expected historical rebal floor to become advisory with good liquidity and local outrate")
	}
}

func TestShouldSoftenHistoricalRebalanceFloorKeepsHardSignals(t *testing.T) {
	tests := []struct {
		name                   string
		floorBaseSrc           string
		recentRebalanceCostPpm int
		recentRebalanceWeak    int
		tags                   []string
		observedOutSignal      bool
		goodLocalLiquidity     bool
		wantSoften             bool
	}{
		{name: "recent cost stays hard", floorBaseSrc: "rebal-recent", recentRebalanceCostPpm: 2461, observedOutSignal: true, goodLocalLiquidity: true},
		{name: "failed rebalance pressure stays hard", floorBaseSrc: "rebal", recentRebalanceWeak: 3, tags: []string{"rebal-fail-pressure"}, observedOutSignal: true, goodLocalLiquidity: true},
		{name: "missing local outrate stays hard", floorBaseSrc: "rebal", observedOutSignal: false, goodLocalLiquidity: true},
		{name: "low liquidity stays hard", floorBaseSrc: "rebal", observedOutSignal: true, goodLocalLiquidity: false},
		{name: "historical blended cost softens", floorBaseSrc: "rebal-blend", observedOutSignal: true, goodLocalLiquidity: true, wantSoften: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldSoftenHistoricalRebalanceFloor(false, "rebal", tc.floorBaseSrc, 444, 444, 1166, 371, tc.goodLocalLiquidity, tc.observedOutSignal, tc.recentRebalanceCostPpm, tc.recentRebalanceWeak, tc.tags)
			if got != tc.wantSoften {
				t.Fatalf("shouldSoftenHistoricalRebalanceFloor = %v, want %v", got, tc.wantSoften)
			}
		})
	}
}

func TestFormatAutofeeFloorSourceIncludesBaseDetail(t *testing.T) {
	tests := []struct {
		name         string
		floorSrc     string
		floorBaseSrc string
		floorBasePpm int
		want         string
	}{
		{name: "collapsed rebal global", floorSrc: "rebal", floorBaseSrc: "rebal-global", floorBasePpm: 426, want: "(rebal-global≈426)"},
		{name: "collapsed outrate memory", floorSrc: "outrate", floorBaseSrc: "outrate-mem", floorBasePpm: 350, want: "(outrate-mem≈350)"},
		{name: "same source", floorSrc: "peg", floorBaseSrc: "peg", floorBasePpm: 500, want: "(peg≈500)"},
		{name: "relaxed source keeps base", floorSrc: "assist", floorBaseSrc: "rebal-global", floorBasePpm: 426, want: "(assist; base rebal-global≈426)"},
		{name: "legacy source", floorSrc: "rebal", want: "(rebal)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatAutofeeFloorSource(tc.floorSrc, tc.floorBaseSrc, tc.floorBasePpm); got != tc.want {
				t.Fatalf("unexpected floor source: got %q want %q", got, tc.want)
			}
		})
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

func TestEffectiveChannelOutRatioKeepsNonOutlierRaw(t *testing.T) {
	got, meta := effectiveChannelOutRatio(1.0, 5_000_000, 5_000_000, 8_000_000, 0.30)
	if math.Abs(got-1.0) > 0.000001 {
		t.Fatalf("expected non-outlier channel to keep raw out ratio, got %.6f want 1.0", got)
	}
	if meta.OutlierSmall || meta.OutlierLarge {
		t.Fatalf("did not expect 5M on 8M average to be classified as outlier: %+v", meta)
	}
}

func TestEffectiveChannelOutRatioAdjustsTrueSmallOutlier(t *testing.T) {
	got, meta := effectiveChannelOutRatio(1.0, 499_000, 500_000, 9_000_000, 0.30)
	if !meta.OutlierSmall {
		t.Fatalf("expected 500k on 9M average to be classified as small outlier: %+v", meta)
	}
	if !(got < meta.Raw) {
		t.Fatalf("expected normalized out ratio to be lower for true small outlier: got %.6f raw %.6f", got, meta.Raw)
	}
}

func TestDynamicUpwardPressureOutRatioPrefersRawForNormalChannels(t *testing.T) {
	meta := outRatioNormalizationMeta{}
	got := dynamicUpwardPressureOutRatio(0.31, 0.24, meta)
	if math.Abs(got-0.31) > 0.000001 {
		t.Fatalf("expected normal channel upward gate to use raw ratio, got %.6f want 0.31", got)
	}
}

func TestDynamicGoodOutRatioDropsWhenNodeLiquidityIsDrained(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	drained := dynamicGoodOutRatio(profile, "drained", 0.30, outRatioNormalizationMeta{})
	full := dynamicGoodOutRatio(profile, "full", 0.80, outRatioNormalizationMeta{})
	if !(drained < profile.BalancedUpOutRatioMin) {
		t.Fatalf("expected drained node threshold below profile base, got %.4f base %.4f", drained, profile.BalancedUpOutRatioMin)
	}
	if !(full > drained) {
		t.Fatalf("expected full node threshold above drained threshold, got full %.4f drained %.4f", full, drained)
	}
}

func TestShouldHoldSeedDrivenUpOnFullChannel(t *testing.T) {
	if !shouldHoldSeedDrivenUpOnFullChannel(false, 0.99, 213, 190, 0, 0, "seed") {
		t.Fatalf("expected seed-driven up hold for full channel without local history")
	}
	if shouldHoldSeedDrivenUpOnFullChannel(false, 0.62, 213, 190, 0, 0, "seed") {
		t.Fatalf("did not expect hold when channel is not full")
	}
	if shouldHoldSeedDrivenUpOnFullChannel(false, 0.99, 213, 190, 25, 0, "seed") {
		t.Fatalf("did not expect hold when local outrate history exists")
	}
	if shouldHoldSeedDrivenUpOnFullChannel(false, 0.99, 213, 190, 0, 120, "seed") {
		t.Fatalf("did not expect hold when local rebalance history exists")
	}
	if shouldHoldSeedDrivenUpOnFullChannel(false, 0.99, 213, 190, 0, 0, "outrate") {
		t.Fatalf("did not expect hold when base cost is not seed")
	}
}

func TestApplyOutnormFallbackUpHold(t *testing.T) {
	target, final, tags := applyOutnormFallbackUpHold(
		false,
		false,
		327,
		352,
		352,
		outRatioNormalizationMeta{OutlierLarge: true},
		true,
		true,
		false,
		false,
		0,
		false,
		false,
	)
	if target != 327 || final != 327 {
		t.Fatalf("expected weak outnorm fallback up move to be held: target=%d final=%d", target, final)
	}
	if len(tags) != 1 || tags[0] != "outnorm-fallback-hold" {
		t.Fatalf("unexpected tags: %#v", tags)
	}
}

func TestApplyHistoryReferenceUpCapCapsToHistory(t *testing.T) {
	target, final, tags := applyHistoryReferenceUpCap(false, false, 130, 378, 314, 110, 285, 0, false, false)
	if target != 285 || final != 285 {
		t.Fatalf("expected up move capped to history reference: target=%d final=%d", target, final)
	}
	if len(tags) != 1 || tags[0] != "history-up-cap" {
		t.Fatalf("unexpected tags: %#v", tags)
	}
}

func TestApplyHistoryReferenceUpCapHoldsWhenAlreadyAboveHistory(t *testing.T) {
	target, final, tags := applyHistoryReferenceUpCap(false, false, 574, 596, 596, 521, 522, 0, false, false)
	if target != 574 || final != 574 {
		t.Fatalf("expected no further rise when current ppm already exceeds history reference: target=%d final=%d", target, final)
	}
	if len(tags) != 1 || tags[0] != "history-up-hold" {
		t.Fatalf("unexpected tags: %#v", tags)
	}
}

func TestSuccessfulRebalanceDoesNotBypassHistoryReferenceUpCap(t *testing.T) {
	target, final, tags := applyHistoryReferenceUpCap(false, false, 130, 378, 314, 110, 285, 1, false, false)
	if target != 285 || final != 285 {
		t.Fatalf("expected successful rebalance not to bypass history cap: target=%d final=%d", target, final)
	}
	if len(tags) != 1 || tags[0] != "history-up-cap" {
		t.Fatalf("unexpected tags: %#v", tags)
	}
}

func TestFailedRebalancePressureBypassesHistoryReferenceUpCap(t *testing.T) {
	target, final, tags := applyHistoryReferenceUpCap(false, false, 130, 378, 314, 110, 285, 0, false, true)
	if target != 378 || final != 314 || len(tags) != 0 {
		t.Fatalf("expected surge confirmation to bypass history cap: target=%d final=%d tags=%v", target, final, tags)
	}
}

func TestShouldPauseOutratePegHeadroomWhenOutrateAlreadyClearsRebalSpread(t *testing.T) {
	if !shouldPauseOutratePegHeadroom(1639, 1217, 301) {
		t.Fatalf("expected peg headroom to pause when outrate already exceeds rebal by 20%%+ with positive margin")
	}
	if shouldPauseOutratePegHeadroom(1400, 1217, 301) {
		t.Fatalf("did not expect peg headroom pause below required outrate/rebal spread")
	}
	if shouldPauseOutratePegHeadroom(1639, 1217, -10) {
		t.Fatalf("did not expect peg headroom pause with negative margin")
	}
}

func TestHasActionableAutofeeMarginRequiresLocalEvidence(t *testing.T) {
	if hasActionableAutofeeMargin(0, 0, false, false, false, false, false, false, 0, false, false) {
		t.Fatalf("did not expect synthetic seed-only margin to be actionable")
	}
	if !hasActionableAutofeeMargin(450, 2, true, false, false, false, false, false, 0, false, false) {
		t.Fatalf("expected observed forward margin to be actionable")
	}
	if !hasActionableAutofeeMargin(0, 0, false, false, false, true, false, false, 0, false, false) {
		t.Fatalf("expected observed rebalance cost margin to be actionable")
	}
	if !hasActionableAutofeeMargin(0, 0, false, false, false, false, false, false, 875, false, false) {
		t.Fatalf("expected recent rebalance cost margin to be actionable")
	}
	if !hasActionableAutofeeMargin(390, 0, false, true, false, false, false, false, 0, false, false) {
		t.Fatalf("expected 21d forward fallback margin to be actionable")
	}
}

func TestShouldProtectRebalanceFloorFromStallRelaxOnRebalanceDependentSink(t *testing.T) {
	ranking := autofeeRankingSnapshot{RebalanceDependence: 70}
	if !shouldProtectRebalanceFloorFromStallRelax("sink", "rebal", 1716, ranking, true, 1654) {
		t.Fatalf("expected rebalance-dependent sink to protect rebal floor from stall relax")
	}
	if shouldProtectRebalanceFloorFromStallRelax("sink", "outrate", 1716, ranking, true, 1654) {
		t.Fatalf("did not expect non-rebal floor source to trigger stall-relax guard")
	}
	if shouldProtectRebalanceFloorFromStallRelax("router", "rebal", 1716, ranking, true, 1654) {
		t.Fatalf("did not expect non-sink channel to trigger stall-relax guard")
	}
}

func TestShouldProtectRebalanceFloorFromStallRelaxWhenRebalDominatesHistory(t *testing.T) {
	ranking := autofeeRankingSnapshot{OutPpm30d: 1620, RebalPpm30d: 1923}
	if !shouldProtectRebalanceFloorFromStallRelax("sink", "rebal-sink", 1716, ranking, true, 1654) {
		t.Fatalf("expected stronger rebalance history to protect rebal floor from stall relax")
	}
	if shouldProtectRebalanceFloorFromStallRelax("sink", "rebal-sink", 0, ranking, true, 1654) {
		t.Fatalf("did not expect stall-relax guard without rebal ppm history")
	}
}

func TestApplyAutofeeRefreshRebalMarkup(t *testing.T) {
	if got := applyAutofeeRefreshRebalMarkup(500, 0.10); got != 550 {
		t.Fatalf("unexpected refresh rebal markup: got %d want 550", got)
	}
	if got := applyAutofeeRefreshRebalMarkup(500, -0.25); got != 500 {
		t.Fatalf("negative markup should clamp to zero: got %d want 500", got)
	}
}

func TestSelectAutofeeRefreshReferencePrefersOutppm(t *testing.T) {
	target, ref, source, ok := selectAutofeeRefreshReference(
		5_000_000,
		forwardStat{FeeMsat: 500_000, AmtMsat: 1_000_000_000, Count: 10},
		forwardStat{FeeMsat: 800_000, AmtMsat: 1_000_000_000, Count: 20},
		rebalStat{FeeMsat: 200_000, AmtMsat: 500_000_000},
		rebalStat{FeeMsat: 400_000, AmtMsat: 500_000_000},
		0.10,
	)
	if !ok {
		t.Fatalf("expected outppm reference to be selected")
	}
	if target != 500 || ref != 500 || source != "outppm7d" {
		t.Fatalf("unexpected outppm selection: target=%d ref=%d source=%q", target, ref, source)
	}
}

func TestSelectAutofeeRefreshReferencePrefersHigherRebalWithoutMarkup(t *testing.T) {
	target, ref, source, ok := selectAutofeeRefreshReference(
		5_000_000,
		forwardStat{FeeMsat: 500_000, AmtMsat: 1_000_000_000, Count: 10},
		forwardStat{},
		rebalStat{FeeMsat: 600_000, AmtMsat: 1_000_000_000},
		rebalStat{},
		0.10,
	)
	if !ok {
		t.Fatalf("expected rebal reference to be selected when it is above outrate")
	}
	if target != 600 || ref != 600 || source != "rebalppm7d" {
		t.Fatalf("unexpected higher rebal selection: target=%d ref=%d source=%q", target, ref, source)
	}
}

func TestSelectAutofeeRefreshReferenceFallsBackToRebalMarkup(t *testing.T) {
	target, ref, source, ok := selectAutofeeRefreshReference(
		5_000_000,
		forwardStat{},
		forwardStat{},
		rebalStat{FeeMsat: 300_000, AmtMsat: 500_000_000},
		rebalStat{},
		0.10,
	)
	if !ok {
		t.Fatalf("expected rebal reference to be selected")
	}
	if target != 660 || ref != 600 || source != "rebalppm7d+10%" {
		t.Fatalf("unexpected rebal selection: target=%d ref=%d source=%q", target, ref, source)
	}
}

func TestSelectAutofeeRefreshReferenceUses21dFallbacks(t *testing.T) {
	target, ref, source, ok := selectAutofeeRefreshReference(
		5_000_000,
		forwardStat{},
		forwardStat{FeeMsat: 600_000, AmtMsat: 1_000_000_000, Count: 6},
		rebalStat{},
		rebalStat{},
		0.10,
	)
	if !ok || target != 600 || ref != 600 || source != "outppm21d" {
		t.Fatalf("unexpected 21d outrate fallback: ok=%v target=%d ref=%d source=%q", ok, target, ref, source)
	}

	target, ref, source, ok = selectAutofeeRefreshReference(
		5_000_000,
		forwardStat{},
		forwardStat{FeeMsat: 600_000, AmtMsat: 1_000_000_000, Count: 6},
		rebalStat{},
		rebalStat{FeeMsat: 800_000, AmtMsat: 1_000_000_000},
		0.10,
	)
	if !ok || target != 800 || ref != 800 || source != "rebalppm21d" {
		t.Fatalf("unexpected 21d higher rebal selection: ok=%v target=%d ref=%d source=%q", ok, target, ref, source)
	}

	target, ref, source, ok = selectAutofeeRefreshReference(
		5_000_000,
		forwardStat{},
		forwardStat{},
		rebalStat{},
		rebalStat{FeeMsat: 250_000, AmtMsat: 250_000_000},
		0.10,
	)
	if !ok || target != 1100 || ref != 1000 || source != "rebalppm21d+10%" {
		t.Fatalf("unexpected 21d rebal fallback: ok=%v target=%d ref=%d source=%q", ok, target, ref, source)
	}
}

func TestApplyAutofeeRefreshSeedFloorProtectsRebalOnlyReference(t *testing.T) {
	target, ref, source, applied := applyAutofeeRefreshSeedFloor(965, 877, "rebalppm21d+10%", 1462)
	if !applied {
		t.Fatalf("expected seed floor to protect rebal-only refresh")
	}
	if target != 1170 || ref != 1170 || source != "rebalppm21d+10%+seed-floor" {
		t.Fatalf("unexpected seed floor refresh: target=%d ref=%d source=%q", target, ref, source)
	}
}

func TestApplyAutofeeRefreshSeedFloorSkipsOutrateReference(t *testing.T) {
	target, ref, source, applied := applyAutofeeRefreshSeedFloor(965, 965, "outppm21d", 1462)
	if applied {
		t.Fatalf("did not expect seed floor on outrate refresh")
	}
	if target != 965 || ref != 965 || source != "outppm21d" {
		t.Fatalf("unexpected outrate refresh mutation: target=%d ref=%d source=%q", target, ref, source)
	}
}

func TestApplyAutofeeRefreshSeedLiquidityAdjustmentRaisesDrainedSeed(t *testing.T) {
	target, ref, source, applied := applyAutofeeRefreshSeedLiquidityAdjustment(1363, 1363, "seed:native", 0.12)
	if !applied {
		t.Fatalf("expected drained seed refresh to be adjusted")
	}
	if target != 1751 || ref != 1363 || source != "seed:native+liq-low" {
		t.Fatalf("unexpected drained seed adjustment: target=%d ref=%d source=%q", target, ref, source)
	}
}

func TestApplyAutofeeRefreshSeedLiquidityAdjustmentLowersFullSeed(t *testing.T) {
	target, ref, source, applied := applyAutofeeRefreshSeedLiquidityAdjustment(2000, 2000, "seed:amboss", 0.90)
	if !applied {
		t.Fatalf("expected full seed refresh to be adjusted")
	}
	if target != 1600 || ref != 2000 || source != "seed:amboss+liq-high" {
		t.Fatalf("unexpected full seed adjustment: target=%d ref=%d source=%q", target, ref, source)
	}
}

func TestApplyAutofeeRefreshSeedLiquidityAdjustmentSkipsNeutralAndLocalRefs(t *testing.T) {
	target, ref, source, applied := applyAutofeeRefreshSeedLiquidityAdjustment(1500, 1500, "seed:native", 0.50)
	if applied || target != 1500 || ref != 1500 || source != "seed:native" {
		t.Fatalf("unexpected neutral seed adjustment: applied=%v target=%d ref=%d source=%q", applied, target, ref, source)
	}

	target, ref, source, applied = applyAutofeeRefreshSeedLiquidityAdjustment(1500, 1500, "outppm7d", 0.05)
	if applied || target != 1500 || ref != 1500 || source != "outppm7d" {
		t.Fatalf("unexpected local reference adjustment: applied=%v target=%d ref=%d source=%q", applied, target, ref, source)
	}
}

func TestShouldAutofeeIdleRefreshChannel(t *testing.T) {
	cfg := AutofeeConfig{IdleRefreshEnabled: true, OperationMode: autofeeOperationModeBalanced}
	if !shouldAutofeeIdleRefreshChannel(cfg, forwardStat{}, inboundStat{}, rebalStat{}) {
		t.Fatalf("expected idle refresh when balanced channel has no 7d movement")
	}
	if shouldAutofeeIdleRefreshChannel(AutofeeConfig{IdleRefreshEnabled: false, OperationMode: autofeeOperationModeBalanced}, forwardStat{}, inboundStat{}, rebalStat{}) {
		t.Fatalf("did not expect idle refresh when disabled")
	}
	if shouldAutofeeIdleRefreshChannel(AutofeeConfig{IdleRefreshEnabled: true, OperationMode: autofeeOperationModeMarketRefill}, forwardStat{}, inboundStat{}, rebalStat{}) {
		t.Fatalf("did not expect idle refresh in market refill mode")
	}
	if shouldAutofeeIdleRefreshChannel(cfg, forwardStat{Count: 1}, inboundStat{}, rebalStat{}) {
		t.Fatalf("did not expect idle refresh with outbound forwards")
	}
	if shouldAutofeeIdleRefreshChannel(cfg, forwardStat{}, inboundStat{Count: 1}, rebalStat{}) {
		t.Fatalf("did not expect idle refresh with inbound forwards")
	}
	if shouldAutofeeIdleRefreshChannel(cfg, forwardStat{}, inboundStat{}, rebalStat{Count: 1}) {
		t.Fatalf("did not expect idle refresh with successful rebalances")
	}
}

func TestAutofeeRefreshTargetMatches(t *testing.T) {
	ch := lndclient.ChannelInfo{
		ChannelPoint: "ABC:1",
		ChannelID:    42,
	}
	if !autofeeRefreshTargetMatches(ch, "abc:1", 0) {
		t.Fatalf("expected channel point match to be case-insensitive")
	}
	if !autofeeRefreshTargetMatches(ch, "", 42) {
		t.Fatalf("expected channel id match")
	}
	if !autofeeRefreshTargetMatches(ch, "ABC:1", 42) {
		t.Fatalf("expected combined channel point and id match")
	}
	if autofeeRefreshTargetMatches(ch, "ABC:1", 43) {
		t.Fatalf("did not expect mismatched channel id to match")
	}
	if autofeeRefreshTargetMatches(ch, "DEF:1", 42) {
		t.Fatalf("did not expect mismatched channel point to match")
	}
}

func TestRefreshSeedForChannelPrefersNative(t *testing.T) {
	pubkey := "02native"
	engine := &autofeeEngine{
		cfg: AutofeeConfig{
			NativeSeedEnabled: true,
			AmbossEnabled:     true,
		},
		nativeSeedCache: map[string]autofeeSeedResult{
			pubkey: {Seed: 777, Tags: []string{"seed:native"}, Ok: true},
		},
	}

	seed, source, err := engine.refreshSeedForChannel(context.Background(), pubkey)
	if err != nil {
		t.Fatalf("unexpected refresh seed error: %v", err)
	}
	if seed != 777 || source != "seed:native" {
		t.Fatalf("expected native refresh seed first, got seed=%.0f source=%q", seed, source)
	}
}

func TestAutofeeNativeSeedSeriesUsesGraphExplorerCorrectedAverage(t *testing.T) {
	normalSample := graphExplorerPolicySample{Ppm: 1000, CapacitySat: 1_000}
	ceilingSample := graphExplorerPolicySample{Ppm: graphExplorerCorrectedCeilingPpm, CapacitySat: 10_000_000}
	buckets := map[string]*graphExplorerFeeHistoryBucket{}
	for i := 0; i < 3; i++ {
		day := time.Date(2026, 6, 20-i, 0, 0, 0, 0, time.UTC)
		buckets[day.Format("2006-01-02")] = &graphExplorerFeeHistoryBucket{
			Day:      day,
			Inbound:  []graphExplorerPolicySample{normalSample, ceilingSample},
			Outbound: []graphExplorerPolicySample{normalSample},
		}
	}

	rawSummary := summarizeGraphExplorerPolicies(buckets["2026-06-20"].Inbound)
	if rawSummary.WeightedAvgPpm < 900_000 {
		t.Fatalf("test setup should have a distorted raw weighted average, got %d", rawSummary.WeightedAvgPpm)
	}

	inboundVals, outboundVals, totalInboundSamples := autofeeNativeSeedSeriesFromBuckets(buckets)
	if totalInboundSamples != 6 {
		t.Fatalf("unexpected inbound sample count: got %d want 6", totalInboundSamples)
	}
	if len(inboundVals) != 3 || len(outboundVals) != 3 {
		t.Fatalf("unexpected series sizes: inbound=%v outbound=%v", inboundVals, outboundVals)
	}
	for _, got := range inboundVals {
		if got != 1000 {
			t.Fatalf("expected corrected inbound seed point to ignore ceiling outlier, got %.0f", got)
		}
	}
	for _, got := range outboundVals {
		if got != 1000 {
			t.Fatalf("unexpected outbound seed point: got %.0f want 1000", got)
		}
	}
}

func TestAutofeeChannelEnabledDefaultsToEnabled(t *testing.T) {
	settings := map[uint64]bool{
		10: true,
		11: false,
	}
	if !autofeeChannelEnabled(settings, 10) {
		t.Fatalf("expected explicitly enabled channel to be enabled")
	}
	if autofeeChannelEnabled(settings, 11) {
		t.Fatalf("expected explicitly disabled channel to be disabled")
	}
	if !autofeeChannelEnabled(settings, 12) {
		t.Fatalf("expected missing channel setting to default to enabled")
	}
	if autofeeChannelEnabled(settings, 0) {
		t.Fatalf("expected zero channel id to be disabled")
	}
}

func TestApplyAutofeeIdleRefreshDecisionHardSetsAndBypassesSkips(t *testing.T) {
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	st := &autofeeChannelState{
		ChannelID:     123,
		LastDir:       "up",
		StalledRounds: 3,
	}
	d := &decision{
		LocalPpm:            1200,
		NewPpm:              1100,
		Target:              900,
		Floor:               1150,
		FloorSrc:            "rebal",
		FloorBasePpm:        1000,
		FloorBaseSrc:        "rebal",
		Tags:                []string{"cooldown", "hold-small", "same-ppm", "stepcap", "neg-margin"},
		State:               st,
		InboundDiscount:     0,
		PrevInboundDiscount: 0,
	}

	applyAutofeeIdleRefreshDecision(d, now, 650, 600, "rebalppm21d+10%")

	if !d.Apply {
		t.Fatalf("expected idle refresh decision to apply")
	}
	if d.NewPpm != 650 || d.Target != 650 || d.TargetRaw != 650 || d.TargetFinal != 650 {
		t.Fatalf("unexpected target fields after hard set: %+v", d)
	}
	if d.Floor != 650 || d.FloorSrc != "idle-refresh" || d.FloorBasePpm != 600 || d.FloorBaseSrc != "rebalppm21d+10%" {
		t.Fatalf("unexpected floor/reference fields after hard set: %+v", d)
	}
	for _, skipped := range []string{"cooldown", "hold-small", "same-ppm", "stepcap"} {
		if containsTag(d.Tags, skipped) {
			t.Fatalf("expected %q tag to be removed, tags=%v", skipped, d.Tags)
		}
	}
	if !containsTag(d.Tags, "idle-refresh") || !containsTag(d.Tags, "idle-refresh:rebalppm21d+10%") || !containsTag(d.Tags, "neg-margin") {
		t.Fatalf("unexpected tags after idle refresh: %v", d.Tags)
	}
	if !st.LastTs.Equal(now) || st.LastDir != "down" || st.StalledRounds != 0 || st.LastPpm != 650 {
		t.Fatalf("unexpected state after idle refresh: %+v", st)
	}
	if !st.LastIdleRefreshTs.Equal(now) || st.LastIdleRefreshPpm != 650 || st.LastIdleRefreshSrc != "rebalppm21d+10%" {
		t.Fatalf("unexpected idle refresh state after hard set: %+v", st)
	}
	if st.ExplorerState.LastReversalDir != "down" || st.ExplorerState.LastReversalTs != now.Unix() {
		t.Fatalf("expected reversal metadata to be updated: %+v", st.ExplorerState)
	}
}

func TestShouldSkipAutofeeIdleRefreshRepeat(t *testing.T) {
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	st := &autofeeChannelState{
		LastIdleRefreshTs:  now.Add(-time.Hour),
		LastIdleRefreshPpm: 650,
	}
	if !shouldSkipAutofeeIdleRefreshRepeat(st, now, 650, 650) {
		t.Fatalf("expected recent same-ppm idle refresh to be skipped")
	}
	if shouldSkipAutofeeIdleRefreshRepeat(st, now, 600, 650) {
		t.Fatalf("expected local ppm drift to allow idle refresh retry")
	}
	if !shouldSkipAutofeeIdleRefreshRepeat(st, now, 649, 650) {
		t.Fatalf("expected recent same-target small-delta idle refresh to be skipped")
	}
	if shouldSkipAutofeeIdleRefreshRepeat(st, now, 650, 700) {
		t.Fatalf("expected target change to allow idle refresh")
	}
	st.LastIdleRefreshTs = now.Add(-time.Duration(autofeeIdleRefreshWindowDays)*24*time.Hour - time.Minute)
	st.LastIdleRefreshPpm = 650
	if shouldSkipAutofeeIdleRefreshRepeat(st, now, 650, 650) {
		t.Fatalf("expected idle refresh to be eligible after the repeat window")
	}
}

func TestShouldHoldAutofeeSmallDeltaUsesAbsoluteAndRelativeFloor(t *testing.T) {
	if got := minAutofeeApplyDeltaPpm(3); got != 5 {
		t.Fatalf("unexpected tiny ppm min delta: got %d want 5", got)
	}
	if got := minAutofeeApplyDeltaPpm(528); got != 6 {
		t.Fatalf("unexpected mid ppm min delta: got %d want 6", got)
	}
	if got := minAutofeeApplyDeltaPpm(1000); got != 10 {
		t.Fatalf("unexpected high ppm min delta: got %d want 10", got)
	}
	if !shouldHoldAutofeeSmallDelta(528, 529) {
		t.Fatalf("expected 1 ppm move to be held")
	}
	if shouldHoldAutofeeSmallDelta(528, 534) {
		t.Fatalf("expected 6 ppm move to be allowed for 528 ppm")
	}
	if !shouldHoldAutofeeSmallDelta(3, 4) {
		t.Fatalf("expected 1 ppm move to be held even with high relative delta")
	}
	if shouldHoldAutofeeSmallDelta(1000, 1010) {
		t.Fatalf("expected 1%% move to be allowed")
	}
}

func TestApplyAutofeeIdleRefreshDecisionHoldsSmallDelta(t *testing.T) {
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	lastChange := now.Add(-24 * time.Hour)
	st := &autofeeChannelState{
		ChannelID:     123,
		LastPpm:       528,
		LastTs:        lastChange,
		LastDir:       "up",
		StalledRounds: 3,
	}
	d := &decision{
		LocalPpm:            528,
		NewPpm:              528,
		Target:              528,
		Tags:                []string{"neg-margin"},
		State:               st,
		InboundDiscount:     0,
		PrevInboundDiscount: 0,
	}

	applyAutofeeIdleRefreshDecision(d, now, 529, 529, "seed:amboss")

	if d.Apply {
		t.Fatalf("did not expect 1 ppm idle refresh delta to apply")
	}
	if d.NewPpm != 528 || d.Target != 529 || d.TargetRaw != 529 || d.TargetFinal != 528 {
		t.Fatalf("unexpected target fields after small-delta hold: %+v", d)
	}
	if !containsTag(d.Tags, "idle-refresh") || !containsTag(d.Tags, "idle-refresh:seed:amboss") ||
		!containsTag(d.Tags, "hold-small") || !containsTag(d.Tags, "small-delta") {
		t.Fatalf("unexpected tags after small-delta idle refresh: %v", d.Tags)
	}
	if !st.LastIdleRefreshTs.Equal(now) || st.LastIdleRefreshPpm != 529 || st.LastIdleRefreshSrc != "seed:amboss" {
		t.Fatalf("unexpected idle refresh debounce state: %+v", st)
	}
	if st.LastPpm != 528 || !st.LastTs.Equal(lastChange) || st.LastDir != "up" || st.StalledRounds != 3 {
		t.Fatalf("small-delta idle refresh should not mutate outbound apply state: %+v", st)
	}
}

func TestFormatAutofeeOutrateSegmentShowsSyntheticReference(t *testing.T) {
	d := &decision{
		OutPpm7d:     635,
		OutPpm7dRaw:  0,
		OutPpmSource: "slow-cycle-30d",
	}
	got := formatAutofeeOutrateSegment(d)
	want := "out_ppm7d≈0 | out_ref≈635(slow-cycle-30d)"
	if got != want {
		t.Fatalf("unexpected outrate segment: got %q want %q", got, want)
	}
}

func TestSelectAutofeeRefreshInboundDiscountBalancedEligible(t *testing.T) {
	cfg := AutofeeConfig{InboundPassiveEnabled: true}
	profile := autofeeProfiles["moderate"]
	discount, source, apply := selectAutofeeRefreshInboundDiscount(
		cfg,
		profile,
		"sink",
		0.08,
		0.08,
		forwardStat{},
		forwardStat{FeeMsat: 1_000_000, AmtMsat: 1_000_000_000, Count: 6},
		5_000_000,
		500,
		1000,
	)
	if !apply || source != "balanced" {
		t.Fatalf("expected balanced inbound refresh to apply, apply=%v source=%q", apply, source)
	}
	if discount <= 0 {
		t.Fatalf("expected positive inbound discount, got %d", discount)
	}
}

func TestSelectAutofeeRefreshInboundDiscountBalancedPreservesIneligible(t *testing.T) {
	cfg := AutofeeConfig{InboundPassiveEnabled: true}
	profile := autofeeProfiles["moderate"]
	discount, source, apply := selectAutofeeRefreshInboundDiscount(
		cfg,
		profile,
		"sink",
		0.30,
		0.30,
		forwardStat{},
		forwardStat{FeeMsat: 1_000_000, AmtMsat: 1_000_000_000, Count: 6},
		5_000_000,
		500,
		1000,
	)
	if apply || source != "" || discount != 0 {
		t.Fatalf("expected ineligible balanced inbound refresh to preserve current setting, apply=%v source=%q discount=%d", apply, source, discount)
	}
}

func TestSelectAutofeeRefreshInboundDiscountBalancedLowSampleRecreatesTarget(t *testing.T) {
	cfg := AutofeeConfig{InboundPassiveEnabled: true}
	profile := autofeeProfiles["moderate"]
	discount, source, apply := selectAutofeeRefreshInboundDiscount(
		cfg,
		profile,
		"sink",
		0.08,
		0.08,
		forwardStat{},
		forwardStat{FeeMsat: 500_000, AmtMsat: 1_000_000_000, Count: 1},
		5_000_000,
		300,
		700,
	)
	if !apply || source != "balanced-low-sample" {
		t.Fatalf("expected balanced low-sample inbound refresh to apply, apply=%v source=%q", apply, source)
	}
	if discount <= 0 {
		t.Fatalf("expected positive low-sample inbound discount, got %d", discount)
	}
}

func TestNormalizeStaleInboundDiscountCapsExtremeDiscount(t *testing.T) {
	target, changed := normalizeStaleInboundDiscount(10_000, 2_889, 0.90)
	if !changed || target != 2601 {
		t.Fatalf("expected stale inbound discount to be capped at 2601, changed=%v target=%d", changed, target)
	}

	target, changed = normalizeStaleInboundDiscount(415, 447, 0.90)
	if !changed || target != 403 {
		t.Fatalf("expected slightly over-cap discount to be normalized, changed=%v target=%d", changed, target)
	}

	target, changed = normalizeStaleInboundDiscount(329, 2679, 0.90)
	if changed || target != 329 {
		t.Fatalf("expected healthy inbound discount to be preserved, changed=%v target=%d", changed, target)
	}
}

func TestNormalizeInboundDiscountMaxRatioOverride(t *testing.T) {
	tests := []struct {
		value float64
		want  float64
	}{
		{value: -0.20, want: 0},
		{value: 0, want: 0},
		{value: 0.01, want: 0.05},
		{value: 0.25, want: 0.25},
		{value: 1.50, want: 1.00},
	}
	for _, tt := range tests {
		if got := normalizeInboundDiscountMaxRatioOverride(tt.value); got != tt.want {
			t.Fatalf("normalizeInboundDiscountMaxRatioOverride(%v)=%v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestAdvanceBalancedInboundDiscountLifecycle(t *testing.T) {
	tests := []struct {
		name           string
		candidate      int
		previous       int
		applied        int
		maxRatio       float64
		previousRounds int
		wantDiscount   int
		wantRounds     int
		wantTags       []string
	}{
		{name: "eligible resets lifecycle", candidate: 400, previous: 600, applied: 1000, maxRatio: 0.90, previousRounds: 8, wantDiscount: 400, wantRounds: 0},
		{name: "first grace round preserves", previous: 600, applied: 1000, maxRatio: 0.90, wantDiscount: 600, wantRounds: 1, wantTags: []string{"inbound-grace"}},
		{name: "grace still enforces current cap", previous: 1000, applied: 500, maxRatio: 0.90, previousRounds: 1, wantDiscount: 450, wantRounds: 2, wantTags: []string{"inbound-normalize", "inbound-grace"}},
		{name: "decays after grace", previous: 600, applied: 1000, maxRatio: 0.90, previousRounds: 2, wantDiscount: 450, wantRounds: 3, wantTags: []string{"inbound-decay"}},
		{name: "minimum decrement reaches zero", previous: 20, applied: 1000, maxRatio: 0.90, previousRounds: 5, wantDiscount: 0, wantRounds: 6, wantTags: []string{"inbound-decay"}},
		{name: "no prior discount resets lifecycle", previous: 0, applied: 1000, maxRatio: 0.90, previousRounds: 5, wantDiscount: 0, wantRounds: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			discount, rounds, tags := advanceBalancedInboundDiscountLifecycle(tt.candidate, tt.previous, tt.applied, tt.maxRatio, tt.previousRounds)
			if discount != tt.wantDiscount || rounds != tt.wantRounds || !reflect.DeepEqual(tags, tt.wantTags) {
				t.Fatalf("got discount=%d rounds=%d tags=%v, want discount=%d rounds=%d tags=%v", discount, rounds, tags, tt.wantDiscount, tt.wantRounds, tt.wantTags)
			}
		})
	}
}

func TestSelectAutofeeRefreshInboundDiscountRejectsNonSink(t *testing.T) {
	cfg := AutofeeConfig{InboundPassiveEnabled: true}
	profile := autofeeProfiles["moderate"]
	discount, source, apply := selectAutofeeRefreshInboundDiscount(
		cfg,
		profile,
		"router",
		0.08,
		0.08,
		forwardStat{},
		forwardStat{FeeMsat: 1_000_000, AmtMsat: 1_000_000_000, Count: 6},
		5_000_000,
		500,
		1000,
	)
	if apply || source != "" || discount != 0 {
		t.Fatalf("expected non-sink refresh to preserve inbound policy, apply=%v source=%q discount=%d", apply, source, discount)
	}
}

func TestInboundFeeUpdateForDiscountClearsStaleDiscount(t *testing.T) {
	enabled, rate := inboundFeeUpdateForDiscount(10_000, 0)
	if !enabled || rate != 0 {
		t.Fatalf("expected explicit zero inbound update, enabled=%v rate=%d", enabled, rate)
	}

	enabled, rate = inboundFeeUpdateForDiscount(0, 450)
	if !enabled || rate != -450 {
		t.Fatalf("expected negative inbound rate update, enabled=%v rate=%d", enabled, rate)
	}

	enabled, rate = inboundFeeUpdateForDiscount(0, 0)
	if enabled || rate != 0 {
		t.Fatalf("expected no inbound update when already zero, enabled=%v rate=%d", enabled, rate)
	}
}

func TestSelectAutofeeRefreshInboundDiscountMarketRefillApplies(t *testing.T) {
	cfg := AutofeeConfig{OperationMode: autofeeOperationModeMarketRefill}
	profile := autofeeProfiles["moderate"]
	discount, source, apply := selectAutofeeRefreshInboundDiscount(
		cfg,
		profile,
		"sink",
		0.08,
		0.08,
		forwardStat{Count: 1},
		forwardStat{},
		5_000_000,
		200,
		1000,
	)
	if !apply || source != "market-refill" {
		t.Fatalf("expected market refill inbound refresh to apply, apply=%v source=%q", apply, source)
	}
	if discount <= 0 {
		t.Fatalf("expected positive market refill inbound discount, got %d", discount)
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

func TestShouldAllowBootstrapSeedSoftFloor(t *testing.T) {
	if !shouldAllowBootstrapSeedSoftFloor(true, 0.20, lowOutNoFlowUpperRatio) {
		t.Fatalf("expected bootstrap channel above threshold to allow seed soft-floor")
	}
	if shouldAllowBootstrapSeedSoftFloor(false, 0.20, lowOutNoFlowUpperRatio) {
		t.Fatalf("did not expect mature non-bootstrap channel to allow seed soft-floor")
	}
	if shouldAllowBootstrapSeedSoftFloor(true, 0.10, lowOutNoFlowUpperRatio) {
		t.Fatalf("did not expect drained bootstrap channel below threshold to allow seed soft-floor")
	}
}

func TestApplySeedShockGuardHoldsMatureIdleShockUntilConfirmed(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	st := &autofeeChannelState{LastSeed: 400}

	seed, tags := applySeedShockGuard(st, profile, 650, nil, true, false)
	if seed != 400 {
		t.Fatalf("expected first seed shock round to hold old seed, got %.0f", seed)
	}
	if !containsTag(tags, "seed:shock-up") || !containsTag(tags, "seed:shock-hold") {
		t.Fatalf("expected shock hold tags, got %v", tags)
	}
	if st.SeedShockPendingPpm != 650 || st.SeedShockRounds != 1 || st.SeedShockDir != "up" {
		t.Fatalf("unexpected pending shock state: %+v", st)
	}

	seed, tags = applySeedShockGuard(st, profile, 652, nil, true, false)
	if seed != 652 {
		t.Fatalf("expected second consistent seed shock round to confirm, got %.0f", seed)
	}
	if !containsTag(tags, "seed:shock-up") || !containsTag(tags, "seed:shock-confirmed") {
		t.Fatalf("expected shock confirmed tags, got %v", tags)
	}
	if st.SeedShockPendingPpm != 0 || st.SeedShockRounds != 0 || st.SeedShockDir != "" {
		t.Fatalf("expected pending shock state to clear after confirmation: %+v", st)
	}
}

func TestApplySeedShockGuardBypassesWithLocalSignal(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	st := &autofeeChannelState{
		LastSeed:            400,
		SeedShockPendingPpm: 650,
		SeedShockRounds:     1,
		SeedShockDir:        "up",
	}

	seed, tags := applySeedShockGuard(st, profile, 700, nil, true, true)
	if seed != 700 {
		t.Fatalf("expected local signal to allow seed shock, got %.0f", seed)
	}
	if len(tags) != 0 {
		t.Fatalf("did not expect shock tags when local signal exists, got %v", tags)
	}
	if st.SeedShockPendingPpm != 0 || st.SeedShockRounds != 0 || st.SeedShockDir != "" {
		t.Fatalf("expected local signal to clear pending shock state: %+v", st)
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

func TestDeriveChannelLiquidityStateBands(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	low, protect, _ := effectiveLowOutThresholds(profile.LowOutThresh, profile.LowOutProtectThresh, "balanced", 0.50)

	tests := []struct {
		name  string
		ratio float64
		want  string
	}{
		{name: "offer ready", ratio: 0.30, want: autofeeLiquidityStateOfferReady},
		{name: "low", ratio: 0.09, want: autofeeLiquidityStateLow},
		{name: "drained", ratio: 0.03, want: autofeeLiquidityStateDrained},
		{name: "extreme drained", ratio: 0.005, want: autofeeLiquidityStateExtremeDrained},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveChannelLiquidityState(tt.ratio, low, protect, profile)
			if got != tt.want {
				t.Fatalf("deriveChannelLiquidityState(%.3f)=%s want %s", tt.ratio, got, tt.want)
			}
		})
	}
}

func TestDeriveChannelLiquidityStateUsesCalibratedLowThreshold(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	drainedLow, drainedProtect, _ := effectiveLowOutThresholds(profile.LowOutThresh, profile.LowOutProtectThresh, "drained", 0.05)
	fullLow, fullProtect, _ := effectiveLowOutThresholds(profile.LowOutThresh, profile.LowOutProtectThresh, "full", 0.85)

	if got := deriveChannelLiquidityState(0.09, drainedLow, drainedProtect, profile); got != autofeeLiquidityStateLow {
		t.Fatalf("expected drained-node calibration to mark 9%% as low, got %s", got)
	}
	if got := deriveChannelLiquidityState(0.09, fullLow, fullProtect, profile); got != autofeeLiquidityStateOfferReady {
		t.Fatalf("expected full-node calibration to keep 9%% offer-ready, got %s", got)
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

func TestRecentRebalanceSignalSurgeConfirmInputsIgnoreSuccess(t *testing.T) {
	capSat := int64(10_000_000)
	minAmtSat := minSurgeConfirmRebalSat(capSat)
	count, amtSat := recentRebalanceSignal{
		Count:  1,
		AmtSat: minAmtSat,
		LastAt: time.Date(2026, 6, 5, 13, 0, 0, 0, time.UTC),
	}.surgeConfirmInputs()

	if hasSurgeConfirmSignal(count, amtSat, capSat) {
		t.Fatalf("did not expect successful rebalance to confirm fee surge")
	}
}

func TestRebalanceSuccessNoUpSignalWindow(t *testing.T) {
	if got := rebalanceSuccessNoUpSignalWindow(2 * time.Hour); got != autofeeRebalanceSuccessNoUpWindow {
		t.Fatalf("expected minimum success no-up window %s, got %s", autofeeRebalanceSuccessNoUpWindow, got)
	}
	if got := rebalanceSuccessNoUpSignalWindow(8 * time.Hour); got != 8*time.Hour {
		t.Fatalf("expected longer success no-up window to be preserved, got %s", got)
	}
}

func TestRecentRebalanceSignalSurgeConfirmInputsUseWeakFailures(t *testing.T) {
	capSat := int64(10_000_000)
	minAmtSat := minSurgeConfirmRebalSat(capSat)
	count, amtSat := recentRebalanceSignal{
		WeakCount:  2,
		WeakAmtSat: minAmtSat * 4,
		WeakLastAt: time.Date(2026, 6, 5, 13, 0, 0, 0, time.UTC),
	}.surgeConfirmInputs()

	if !hasSurgeConfirmSignal(count, amtSat, capSat) {
		t.Fatalf("expected repeated failed rebalances to confirm fee surge, count=%d amt=%d", count, amtSat)
	}
}

func TestRecentRebalanceSignalSurgeConfirmInputsSuccessClearsOlderWeakFailures(t *testing.T) {
	capSat := int64(10_000_000)
	minAmtSat := minSurgeConfirmRebalSat(capSat)
	count, amtSat := recentRebalanceSignal{
		Count:      1,
		AmtSat:     minAmtSat,
		LastAt:     time.Date(2026, 6, 5, 13, 0, 0, 0, time.UTC),
		WeakCount:  4,
		WeakAmtSat: minAmtSat * 8,
		WeakLastAt: time.Date(2026, 6, 5, 12, 30, 0, 0, time.UTC),
	}.surgeConfirmInputs()

	if hasSurgeConfirmSignal(count, amtSat, capSat) {
		t.Fatalf("did not expect weak failures before a successful rebalance to confirm fee surge")
	}
}

func TestRecentRebalanceSignalSurgeConfirmInputsAllowWeakFailuresAfterSuccess(t *testing.T) {
	capSat := int64(10_000_000)
	minAmtSat := minSurgeConfirmRebalSat(capSat)
	count, amtSat := recentRebalanceSignal{
		Count:      1,
		AmtSat:     minAmtSat,
		LastAt:     time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC),
		WeakCount:  2,
		WeakAmtSat: minAmtSat * 4,
		WeakLastAt: time.Date(2026, 6, 5, 13, 0, 0, 0, time.UTC),
	}.surgeConfirmInputs()

	if !hasSurgeConfirmSignal(count, amtSat, capSat) {
		t.Fatalf("expected weak failures after a successful rebalance to confirm a new fee surge, count=%d amt=%d", count, amtSat)
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

	if hasFloorRebalSignal(0, capSat, floorRebalMinSuccessCount) {
		t.Fatalf("expected false without rebalance volume")
	}
	if hasFloorRebalSignal(minAmtSat-1, capSat, floorRebalMinSuccessCount) {
		t.Fatalf("expected false when rebalance amount is below floor threshold")
	}
	if hasFloorRebalSignal(minAmtSat, capSat, floorRebalMinSuccessCount-1) {
		t.Fatalf("expected false when rebalance success count is below confidence threshold")
	}
	if !hasFloorRebalSignal(minAmtSat, capSat, floorRebalMinSuccessCount) {
		t.Fatalf("expected true when rebalance amount meets floor threshold")
	}
	if !hasFloorRebalSignal(1, 0, floorRebalMinSuccessCount) {
		t.Fatalf("expected true with positive amount when capacity is unknown")
	}
}

func TestRecentRebalanceCostFloorUsesSingleLargeSuccess(t *testing.T) {
	sig := recentRebalanceSignal{
		Count:   1,
		AmtSat:  1_390_935,
		FeeMsat: 3_422_700,
	}
	if got := sig.costPpm(); got != 2461 {
		t.Fatalf("unexpected recent rebalance cost ppm: got %d want 2461", got)
	}
	if !hasRecentRebalanceCostFloor(sig, 10_000_000) {
		t.Fatalf("expected one substantial recent rebalance to activate cost floor")
	}
}

func TestRecentRebalanceCostFloorIgnoresTinySuccess(t *testing.T) {
	sig := recentRebalanceSignal{
		Count:   1,
		AmtSat:  10_000,
		FeeMsat: 50_000,
	}
	if hasRecentRebalanceCostFloor(sig, 10_000_000) {
		t.Fatalf("did not expect tiny recent rebalance to activate cost floor")
	}
}

func TestApplyRecentRebalanceCostHardFloor(t *testing.T) {
	if got, applied := applyRecentRebalanceCostHardFloor(2000, 2461, 10, 4000); got != 2461 || !applied {
		t.Fatalf("expected recent rebalance cost hard floor, got ppm=%d applied=%v", got, applied)
	}
	if got, applied := applyRecentRebalanceCostHardFloor(2600, 2461, 10, 4000); got != 2600 || applied {
		t.Fatalf("did not expect hard floor when final ppm already covers cost, got ppm=%d applied=%v", got, applied)
	}
	if got, applied := applyRecentRebalanceCostHardFloor(2000, 5000, 10, 4000); got != 4000 || !applied {
		t.Fatalf("expected recent rebalance hard floor to respect max ppm, got ppm=%d applied=%v", got, applied)
	}
}

func TestApplyNegMarginProtectedDownAllowsFloorSafeReduction(t *testing.T) {
	protected := deriveNegMarginProtectedFloor(2457, 2174, 0, 10, 4000)
	if protected != 2457 {
		t.Fatalf("unexpected protected floor: got %d want 2457", protected)
	}

	target, tag := applyNegMarginProtectedDown(2800, 3417, protected, false)
	if target != 2800 || tag != "neg-margin-floor-down" {
		t.Fatalf("expected floor-safe reduction, got target=%d tag=%q", target, tag)
	}
}

func TestApplyNegMarginProtectedDownClampsToProtectedFloor(t *testing.T) {
	target, tag := applyNegMarginProtectedDown(2200, 3417, 2457, false)
	if target != 2457 || tag != "neg-margin-floor-down" {
		t.Fatalf("expected reduction to clamp at protected floor, got target=%d tag=%q", target, tag)
	}
}

func TestApplyNegMarginProtectedDownBlocksStrongPressure(t *testing.T) {
	target, tag := applyNegMarginProtectedDown(2800, 3417, 2457, true)
	if target != 3417 || tag != "no-down-neg-margin" {
		t.Fatalf("expected strong pressure to block reduction, got target=%d tag=%q", target, tag)
	}
}

func TestApplyNegMarginProtectedDownBlocksWithoutHeadroom(t *testing.T) {
	target, tag := applyNegMarginProtectedDown(2400, 2457, 2457, false)
	if target != 2457 || tag != "no-down-neg-margin" {
		t.Fatalf("expected no headroom above protected floor to block reduction, got target=%d tag=%q", target, tag)
	}
}

func TestApplyStaleNegMarginDownDecayAllowsSmallStepWithGoodLiquidity(t *testing.T) {
	got, tags := applyStaleNegMarginDownDecay(
		false,
		1635,
		1000,
		1635,
		0,
		outRatioNormalizationMeta{Raw: 0.29, Effective: 0.29},
		0.20,
		true,
		-1600,
		false,
		0,
		0,
		0,
		false,
		false,
		false,
		48,
		10,
	)
	if got != 1575 {
		t.Fatalf("expected one capped stale decay step, got %d", got)
	}
	if !containsTag(tags, "neg-margin-stale-down") || !containsTag(tags, "stale-cost-down") {
		t.Fatalf("expected stale decay tags, got %+v", tags)
	}
}

func TestApplyStaleNegMarginDownDecayBlocksPressureAndRecentFill(t *testing.T) {
	meta := outRatioNormalizationMeta{Raw: 0.29, Effective: 0.29}
	if got, tags := applyStaleNegMarginDownDecay(false, 1635, 1000, 1635, 0, meta, 0.20, true, -1600, false, 1, 0, 0, false, false, false, 48, 10); got != 1000 || len(tags) != 0 {
		t.Fatalf("expected relevant fill to block stale decay, got %d tags=%+v", got, tags)
	}
	if got, tags := applyStaleNegMarginDownDecay(false, 1635, 1000, 1635, 0, meta, 0.20, true, -1600, false, 0, 2, 0, false, false, false, 48, 10); got != 1000 || len(tags) != 0 {
		t.Fatalf("expected failed pressure to block stale decay, got %d tags=%+v", got, tags)
	}
	if got, tags := applyStaleNegMarginDownDecay(false, 1635, 1000, 1635, 0, meta, 0.20, true, -1600, false, 0, 0, 900, false, false, false, 48, 10); got != 1000 || len(tags) != 0 {
		t.Fatalf("expected recent cost floor to block stale decay, got %d tags=%+v", got, tags)
	}
}

func TestDeriveNegMarginProtectedFloorIncludesRecentCost(t *testing.T) {
	protected := deriveNegMarginProtectedFloor(2457, 2174, 2461, 10, 4000)
	if protected != 2461 {
		t.Fatalf("expected recent cost to raise protected floor, got %d", protected)
	}
}

func TestRelevantRecentRebalanceFill(t *testing.T) {
	if isRelevantRecentRebalanceFill(67_258, 300_000, 10_000_000) {
		t.Fatalf("did not expect a small partial fill to be relevant")
	}
	if !isRelevantRecentRebalanceFill(160_000, 300_000, 10_000_000) {
		t.Fatalf("expected fill above half target to be relevant")
	}
	if !isRelevantRecentRebalanceFill(120_000, 1_000_000, 10_000_000) {
		t.Fatalf("expected fill above one percent capacity to be relevant")
	}
	if !isRelevantRecentRebalanceFill(300_000, 0, 0) {
		t.Fatalf("expected large fill with missing target/capacity to be relevant")
	}
}

func TestRecentRebalanceSignalSmallPartialDoesNotSuppressWeakPressure(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	sig := recentRebalanceSignal{
		Count:            1,
		AmtSat:           67_258,
		FeeMsat:          80_000,
		LastAt:           now,
		RelevanceChecked: true,
		WeakCount:        3,
		WeakAmtSat:       900_000,
		WeakLastAt:       now.Add(-30 * time.Minute),
	}
	count, amt := sig.surgeConfirmInputs()
	if count <= 0 || amt <= 0 {
		t.Fatalf("expected weak pressure to survive small partial fill, got count=%d amt=%d", count, amt)
	}

	sig.RelevantCount = 1
	sig.RelevantAmtSat = 300_000
	sig.RelevantLastAt = now
	count, amt = sig.surgeConfirmInputs()
	if count != 0 || amt != 0 {
		t.Fatalf("expected relevant fill to suppress older weak pressure, got count=%d amt=%d", count, amt)
	}
}

func TestRebalFloorConfidence(t *testing.T) {
	if got := rebalFloorConfidence(0); got != 0 {
		t.Fatalf("expected zero confidence without settled rebalances: got %.2f", got)
	}
	if got := rebalFloorConfidence(1); got != 0.5 {
		t.Fatalf("expected half confidence for one settled rebalance: got %.2f", got)
	}
	if got := rebalFloorConfidence(2); got != 1 {
		t.Fatalf("expected full confidence at threshold: got %.2f", got)
	}
	if got := rebalFloorConfidence(5); got != 1 {
		t.Fatalf("expected confidence to cap at one: got %.2f", got)
	}
}

func TestApplyHTLCNoisySampleDampening(t *testing.T) {
	minFails, rate, noisy, ratio := applyHTLCNoisySampleDampening(100, 0, 2, 0.10)
	if !noisy || minFails != 3 || math.Abs(rate-0.15) > 0.000001 || ratio != 0 {
		t.Fatalf("expected zero-classified sample to dampen: minFails=%d rate=%.4f noisy=%v ratio=%.2f", minFails, rate, noisy, ratio)
	}

	minFails, rate, noisy, ratio = applyHTLCNoisySampleDampening(100, 2, 2, 0.10)
	if !noisy {
		t.Fatalf("expected noisy sample dampening to apply")
	}
	if ratio != 0.02 {
		t.Fatalf("unexpected classified ratio: got %.2f want %.2f", ratio, 0.02)
	}
	if minFails != 3 {
		t.Fatalf("unexpected dampened min forward fails: got %d want %d", minFails, 3)
	}
	if math.Abs(rate-0.15) > 0.000001 {
		t.Fatalf("unexpected dampened forward rate: got %.4f want %.4f", rate, 0.15)
	}

	minFails, rate, noisy, ratio = applyHTLCNoisySampleDampening(100, 10, 2, 0.10)
	if noisy {
		t.Fatalf("did not expect dampening when classified ratio is healthy")
	}
	if minFails != 2 || rate != 0.10 || ratio != 0.10 {
		t.Fatalf("unexpected unchanged thresholds: minFails=%d rate=%.2f ratio=%.2f", minFails, rate, ratio)
	}
}

func TestLargeGapStepCapBoost(t *testing.T) {
	profile := autofeeProfiles["moderate"]

	boost, strong := largeGapStepCapBoost(profile, 1000, 1200)
	if boost != 0 || strong {
		t.Fatalf("did not expect boost below minimum gap: boost=%.2f strong=%v", boost, strong)
	}

	boost, strong = largeGapStepCapBoost(profile, 1000, 1300)
	if math.Abs(boost-0.03) > 0.000001 || strong {
		t.Fatalf("expected regular moderate gap boost: boost=%.2f strong=%v", boost, strong)
	}

	boost, strong = largeGapStepCapBoost(profile, 1000, 1500)
	if math.Abs(boost-0.06) > 0.000001 || !strong {
		t.Fatalf("expected strong moderate gap boost: boost=%.2f strong=%v", boost, strong)
	}

	boost, strong = largeGapStepCapBoost(profile, 1000, 1000)
	if boost != 0 || strong {
		t.Fatalf("did not expect boost for stable target: boost=%.2f strong=%v", boost, strong)
	}
}

func TestSurgePressureStepCapLimitsLargeGapBoost(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	profile.StepCap = 0.12
	capFrac := profile.StepCap
	if boost, _ := largeGapStepCapBoost(profile, 1000, 1800); boost > 0 {
		capFrac = math.Max(capFrac, profile.StepCap+boost)
	}

	if !shouldLimitSurgePressureStepUp(false, 1000, 1800, true, true, false, 0, 2) {
		t.Fatalf("expected confirmed failed-rebalance surge to use gradual cap")
	}
	limit := surgePressureStepCapFrac(profile)
	if capFrac > limit {
		capFrac = limit
	}

	if math.Abs(capFrac-surgePressureStepCapMaxFrac) > 0.000001 {
		t.Fatalf("expected surge cap %.2f, got %.2f", surgePressureStepCapMaxFrac, capFrac)
	}
	if got := applyStepCap(1000, 1800, capFrac, 5, 1000); got != 1080 {
		t.Fatalf("expected 8%% gradual step to 1080 ppm, got %d", got)
	}
}

func TestSurgePressureFollowUpUsesSmallCap(t *testing.T) {
	now := time.Date(2026, 6, 8, 15, 0, 0, 0, time.UTC)
	st := &autofeeChannelState{
		LastDir: "up",
		LastTs:  now.Add(-4 * time.Hour),
	}
	profile := autofeeProfiles["moderate"]
	capFrac := surgePressureStepCapFrac(profile)
	if shouldUseSurgePressureFollowUpCap(st, now) {
		capFrac = math.Min(capFrac, surgePressureFollowUpStepCapFrac)
	}

	if math.Abs(capFrac-surgePressureFollowUpStepCapFrac) > 0.000001 {
		t.Fatalf("expected follow-up cap %.2f, got %.2f", surgePressureFollowUpStepCapFrac, capFrac)
	}
	if got := applyStepCap(3000, 4000, capFrac, 5, 3000); got != 3090 {
		t.Fatalf("expected 3%% follow-up step to 3090 ppm, got %d", got)
	}
}

func TestOrganicAutofeeRefillDetectsForwardInboundWithoutRebalance(t *testing.T) {
	forwardIn := inboundStat{AmtMsat: 16_700_000_000, Count: 80}
	forwardOut := forwardStat{AmtMsat: 16_000_000_000, Count: 80}
	if !hasOrganicAutofeeRefill(forwardIn, forwardOut, rebalStat{}, 10_000_000) {
		t.Fatalf("expected strong forward inbound with no rebalance to be organic refill")
	}

	rebalIn := rebalStat{AmtMsat: 2_000_000_000, Count: 1}
	if hasOrganicAutofeeRefill(forwardIn, forwardOut, rebalIn, 10_000_000) {
		t.Fatalf("expected large rebalance-in amount to disable organic refill")
	}
}

func TestCapRevfloorForOrganicRefillUsesLocalReference(t *testing.T) {
	capped, applied := capRevfloorForOrganicRefill(338, revfloorLocalReferencePpm(12, 0, 14), 45, 10, 2000, true, false, 0, -3, 0)
	if !applied || capped != 36 {
		t.Fatalf("expected revfloor to cap at local reference x%.0f: capped=%d applied=%v", revfloorOrganicLocalRefMult, capped, applied)
	}

	unchanged, applied := capRevfloorForOrganicRefill(338, 14, 45, 10, 2000, true, false, 0, -3, 865)
	if applied || unchanged != 338 {
		t.Fatalf("expected recent rebalance cost to preserve revfloor: capped=%d applied=%v", unchanged, applied)
	}
}

func TestCapRevfloorForOrganicRefillTightensOnHighLiquidity(t *testing.T) {
	capped, applied := capRevfloorForOrganicRefill(356, revfloorLocalReferencePpm(132, 83, 158), 188, 10, 2000, true, true, 0, 41, 0)
	if !applied || capped != 188 {
		t.Fatalf("expected high-liquidity organic refill cap at local reference x%.2f: capped=%d applied=%v", revfloorOrganicHighLiquidityRefMult, capped, applied)
	}

	capped, applied = capRevfloorForOrganicRefill(356, 158, 230, 10, 2000, true, true, 0, 41, 0)
	if !applied || capped != 230 {
		t.Fatalf("expected high-liquidity cap not to go below target: capped=%d applied=%v", capped, applied)
	}
}

func TestCapDetachedRevfloorAllowsLowRecentCostButProtectsHighCost(t *testing.T) {
	capped, applied := capDetachedRevfloor(333, revfloorLocalReferencePpm(107, 88, 128), 157, 10, 2000, 0.01, 6, 92)
	if !applied || capped != 161 {
		t.Fatalf("expected detached revfloor to cap near local references: capped=%d applied=%v", capped, applied)
	}

	unchanged, applied := capDetachedRevfloor(333, 128, 157, 10, 2000, 0.01, 6, 220)
	if applied || unchanged != 333 {
		t.Fatalf("expected high recent cost to preserve revfloor: capped=%d applied=%v", unchanged, applied)
	}

	capped, applied = capDetachedRevfloor(333, 128, 157, 10, 2000, 0.01, 6, 92)
	if !applied || capped != 192 {
		t.Fatalf("expected local references to cap revfloor even when liquidity is low: capped=%d applied=%v", capped, applied)
	}
}

func TestReliableRevfloorLocalReferenceRequiresLocalEvidence(t *testing.T) {
	if !hasReliableRevfloorLocalReference(107, outrateFloorMinFwds, false, 0, false, false, 0) {
		t.Fatalf("expected sufficient outrate sample to be a reliable revfloor reference")
	}
	if hasReliableRevfloorLocalReference(107, outrateFloorMinFwds-1, false, 0, false, false, 0) {
		t.Fatalf("did not expect low outrate sample to be a reliable revfloor reference")
	}
	if !hasReliableRevfloorLocalReference(0, 0, false, 88, true, false, 0) {
		t.Fatalf("expected floor-quality rebalance signal to be reliable")
	}
	if !hasReliableRevfloorLocalReference(0, 0, false, 0, false, false, 92) {
		t.Fatalf("expected recent rebalance cost to be reliable")
	}
}

func TestRelaxStaleNoFlowAdvisoryFloorUsesEffectiveLiquidity(t *testing.T) {
	meta := outRatioNormalizationMeta{
		Raw:       0.31,
		Effective: 0.31,
	}
	floor, src, tags := relaxStaleNoFlowAdvisoryFloor(
		false,
		607,
		583,
		607,
		"seed-synthetic",
		meta,
		0.20,
		true,
		0,
		0,
		0,
		0,
		0,
		false,
		false,
		false,
		30*24,
		defaultBootstrapHours,
		2,
		10,
	)
	if len(tags) > 0 || floor != 607 || src != "seed-synthetic" {
		t.Fatalf("did not expect recent stale/no-flow relax: floor=%d src=%s tags=%v", floor, src, tags)
	}

	floor, src, tags = relaxStaleNoFlowAdvisoryFloor(
		false,
		607,
		583,
		607,
		"seed-synthetic",
		meta,
		0.20,
		true,
		0,
		0,
		0,
		0,
		0,
		false,
		false,
		false,
		30*24,
		defaultBootstrapHours,
		30,
		10,
	)
	if floor != 583 || src != "stale-noflow" || !containsTag(tags, "stale-noflow-down") || !containsTag(tags, "advisory-floor-relax") {
		t.Fatalf("expected effective-liquidity stale/no-flow floor relax: floor=%d src=%s tags=%v", floor, src, tags)
	}
}

func TestRelaxStaleNoFlowAdvisoryFloorSmallOutnormIsSlower(t *testing.T) {
	meta := outRatioNormalizationMeta{
		Raw:          0.81,
		Effective:    0.15,
		CapRel:       0.01,
		OutlierSmall: true,
	}
	floor, src, tags := relaxStaleNoFlowAdvisoryFloor(
		false,
		645,
		620,
		645,
		"seed-synthetic",
		meta,
		0.20,
		true,
		0,
		0,
		0,
		0,
		0,
		false,
		false,
		false,
		30*24,
		defaultBootstrapHours,
		72,
		10,
	)
	if len(tags) > 0 || floor != 645 || src != "seed-synthetic" {
		t.Fatalf("small outnorm channel should require longer stale window: floor=%d src=%s tags=%v", floor, src, tags)
	}

	floor, src, tags = relaxStaleNoFlowAdvisoryFloor(
		false,
		645,
		620,
		645,
		"seed-synthetic",
		meta,
		0.20,
		true,
		0,
		0,
		0,
		0,
		0,
		false,
		false,
		false,
		30*24,
		defaultBootstrapHours,
		106,
		10,
	)
	if floor != 629 || src != "stale-noflow" || !containsTag(tags, "stale-noflow-small-down") || !containsTag(tags, "outnorm-small-down-cap") {
		t.Fatalf("expected capped small-channel relax after long stale window: floor=%d src=%s tags=%v", floor, src, tags)
	}
}

func TestRelaxStaleNoFlowAdvisoryFloorKeepsHardCostFloor(t *testing.T) {
	meta := outRatioNormalizationMeta{Raw: 0.35, Effective: 0.35}
	floor, src, tags := relaxStaleNoFlowAdvisoryFloor(
		false,
		900,
		820,
		1000,
		"rebal-recent",
		meta,
		0.20,
		true,
		0,
		0,
		0,
		0,
		865,
		false,
		false,
		false,
		30*24,
		defaultBootstrapHours,
		120,
		10,
	)
	if len(tags) > 0 || floor != 1000 || src != "rebal-recent" {
		t.Fatalf("hard recent rebalance floor must not relax: floor=%d src=%s tags=%v", floor, src, tags)
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

func TestApplyNoisySurgeFollowUpUpHoldBlocksSecondNoisyStep(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	got, held := applyNoisySurgeFollowUpUpHold(
		1904,
		2322,
		1717,
		"up",
		now.Add(-2*time.Hour),
		now,
		3*time.Hour,
		true,
		true,
		false,
		false,
		0,
		true,
		false,
	)
	if got != 1904 || !held {
		t.Fatalf("expected noisy follow-up surge to hold at local ppm, got %d held=%v", got, held)
	}
}

func TestApplyNoisySurgeFollowUpUpHoldAllowsCurrentHTLCHot(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	got, held := applyNoisySurgeFollowUpUpHold(
		1561,
		1904,
		1717,
		"up",
		now.Add(-2*time.Hour),
		now,
		3*time.Hour,
		false,
		true,
		true,
		true,
		0,
		true,
		false,
	)
	if got != 1904 || held {
		t.Fatalf("expected current HTLC hot signal to allow step, got %d held=%v", got, held)
	}
}

func TestApplyNoisySurgeFollowUpUpHoldAllowsOutsideWindow(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	got, held := applyNoisySurgeFollowUpUpHold(
		1904,
		2322,
		1717,
		"up",
		now.Add(-4*time.Hour),
		now,
		3*time.Hour,
		true,
		true,
		false,
		false,
		0,
		true,
		false,
	)
	if got != 2322 || held {
		t.Fatalf("expected noisy follow-up outside window to pass, got %d held=%v", got, held)
	}
}

func TestApplyNoisySurgeFollowUpUpHoldAllowsRiseToFloor(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	got, held := applyNoisySurgeFollowUpUpHold(
		1500,
		1904,
		1717,
		"up",
		now.Add(-2*time.Hour),
		now,
		3*time.Hour,
		true,
		true,
		false,
		false,
		0,
		true,
		false,
	)
	if got != 1717 || !held {
		t.Fatalf("expected noisy follow-up hold to still allow floor, got %d held=%v", got, held)
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

func TestShouldFastTrackDrainedFailedUpReversal(t *testing.T) {
	if !shouldFastTrackDrainedFailedUpReversal(0.05, 0.10, 171, 275, 0, 2, true, false, 60) {
		t.Fatalf("expected drained failed channel with confirmed surge pressure to fast-track upward reversal")
	}
	if shouldFastTrackDrainedFailedUpReversal(0.05, 0.10, 171, 275, 1, 2, true, false, 60) {
		t.Fatalf("did not expect fast-track after a relevant recent fill")
	}
	if shouldFastTrackDrainedFailedUpReversal(0.15, 0.10, 171, 275, 0, 2, true, false, 60) {
		t.Fatalf("did not expect fast-track when channel is not drained")
	}
	if shouldFastTrackDrainedFailedUpReversal(0.05, 0.10, 171, 275, 0, 1, true, false, 60) {
		t.Fatalf("did not expect fast-track without repeated failed rebalance pressure")
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
	capped, tags := capBalancedFloorDrivenUp(profile, "router", 0.32, 0.20, 1710, 1533, 2012)
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
	capped, tags := capBalancedFloorDrivenUp(profile, "sink", 0.08, 0.20, 1000, 950, 1180)
	if capped != 1180 {
		t.Fatalf("expected drained sink to keep stronger upward floor, got %d", capped)
	}
	if len(tags) != 0 {
		t.Fatalf("did not expect cap tag for drained sink, got %+v", tags)
	}
}

func TestCapBalancedFloorDrivenUpForMidLiquiditySink(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	capped, tags := capBalancedFloorDrivenUp(profile, "sink", 0.24, 0.20, 1784, 1592, 2369)
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
	anchored, tags := applyOutrateTargetAnchor(profile, 512, 1086, 0.32, 0.20, 9)
	if anchored != 869 {
		t.Fatalf("unexpected anchored target: got %d want 869", anchored)
	}
	if len(tags) != 1 || tags[0] != "outrate-target-anchor" {
		t.Fatalf("unexpected outrate anchor tags: %+v", tags)
	}
}

func TestApplyOutrateTargetAnchorBypassesLowLiquidity(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	anchored, tags := applyOutrateTargetAnchor(profile, 512, 1086, 0.12, 0.20, 9)
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

func TestShouldBypassAutofeeSettlingForDrainedFailedRebalanceUp(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	if !shouldBypassAutofeeSettlingForDrainedFailedRebalanceUp(0.05, profile.LowOutProtectThresh, 400, 480, 0, 3, true, false, 20) {
		t.Fatalf("expected drained failed-rebalance up pressure to bypass settling")
	}
	if shouldBypassAutofeeSettlingForDrainedFailedRebalanceUp(0.05, profile.LowOutProtectThresh, 400, 480, 1, 3, true, false, 20) {
		t.Fatalf("did not expect bypass when a relevant rebalance succeeded")
	}
	if shouldBypassAutofeeSettlingForDrainedFailedRebalanceUp(0.25, profile.LowOutProtectThresh, 400, 480, 0, 3, true, false, 20) {
		t.Fatalf("did not expect bypass for non-drained channel")
	}
	if shouldBypassAutofeeSettlingForDrainedFailedRebalanceUp(0.05, profile.LowOutProtectThresh, 400, 430, 0, 3, true, false, 7.5) {
		t.Fatalf("did not expect bypass for small upward gap")
	}
	if shouldBypassAutofeeSettlingForDrainedFailedRebalanceUp(0.05, profile.LowOutProtectThresh, 400, 480, 0, 1, true, false, 20) {
		t.Fatalf("did not expect bypass without repeated failed rebalance pressure")
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

func TestShouldBypassStrictCooldownUp(t *testing.T) {
	profile := autofeeProfiles["moderate"]

	if shouldBypassStrictCooldownUp(profile, 0, false, 0, 0, 500, 400, 0, 400, "outrate", false, nil) {
		t.Fatalf("did not expect plain forward activity to bypass strict cooldown")
	}
	if shouldBypassStrictCooldownUp(profile, 0, true, 2, 19, 2000, 400, 0, 400, "outrate", false, nil) {
		t.Fatalf("did not expect weak liquidity-hot signal to bypass strict cooldown on high-fee channel")
	}
	if !shouldBypassStrictCooldownUp(profile, 0, true, 4, 10, 2000, 400, 0, 400, "outrate", false, nil) {
		t.Fatalf("expected strong liquidity-hot signal to bypass strict cooldown")
	}
	if shouldBypassStrictCooldownUp(profile, 0, true, 4, 10, 2000, 400, 0, 400, "outrate", true, nil) {
		t.Fatalf("did not expect strong liquidity-hot signal to bypass strict cooldown with good local liquidity")
	}
	if !shouldBypassStrictCooldownUp(profile, 0, true, 4, 10, 2000, 400, 0, 400, "outrate", true, []string{"rebal-fail-pressure"}) {
		t.Fatalf("expected confirmed rebalance-fail pressure to bypass strict cooldown")
	}
	if !shouldBypassStrictCooldownUp(profile, 0, true, 1, 10, 500, 400, 0, 400, "outrate", false, nil) {
		t.Fatalf("expected low-fee liquidity-hot signal to bypass strict cooldown")
	}
	if !shouldBypassStrictCooldownUp(profile, 1, false, 0, 0, 480, 400, 0, 400, "outrate", false, nil) {
		t.Fatalf("expected recent rebalance near the historical reference to bypass strict cooldown")
	}
	if shouldBypassStrictCooldownUp(profile, 1, false, 0, 0, 930, 409, 430, 430, "rebal", false, nil) {
		t.Fatalf("did not expect detached recent-rebalance channel to bypass strict cooldown")
	}
}

func TestShouldSoftenNegMarginSurge(t *testing.T) {
	if !shouldSoftenNegMarginSurge(1200, 3, nil) {
		t.Fatalf("expected positive 7d profit with forwards and no failed rebalance pressure to soften neg-margin surge")
	}
	if shouldSoftenNegMarginSurge(0, 3, nil) {
		t.Fatalf("did not expect zero profit to soften neg-margin surge")
	}
	if shouldSoftenNegMarginSurge(1200, 0, nil) {
		t.Fatalf("did not expect no-flow channel to soften neg-margin surge")
	}
	if !shouldSoftenNegMarginSurge(1200, 3, []string{"rebal-attempt"}) {
		t.Fatalf("expected weak rebalance attempts to keep profitable neg-margin surge softened")
	}
	if shouldSoftenNegMarginSurge(1200, 3, []string{"rebal-fail-pressure"}) {
		t.Fatalf("did not expect confirmed failed-rebalance pressure to soften neg-margin surge")
	}
}

func TestCapHighFeePressureStepUpLimitsProfitableWeakPressure(t *testing.T) {
	got, tags := capHighFeePressureStepUp(
		2000,
		2500,
		1200,
		0,
		false,
		2,
		19,
		false,
		[]string{"htlc-liquidity-hot", "surge+20%"},
	)
	want := 2000 + highFeePressureStepMaxPpm/2
	if got != want {
		t.Fatalf("expected profitable high-fee weak pressure to cap at %d, got %d", want, got)
	}
	if len(tags) != 1 || tags[0] != "high-fee-pressure-cap" {
		t.Fatalf("unexpected cap tags: %+v", tags)
	}
}

func TestCapHighFeePressureStepUpTreatsWeakRebalanceAttemptAsGradual(t *testing.T) {
	got, tags := capHighFeePressureStepUp(
		1903,
		2226,
		2566,
		2,
		false,
		0,
		29,
		false,
		[]string{"rebal-attempt", "htlc-forward-hot", "surge+16%"},
	)
	want := 1903 + highFeePressureStepMaxPpm/2
	if got != want {
		t.Fatalf("expected profitable high-fee weak rebalance attempt to cap at %d, got %d", want, got)
	}
	if len(tags) != 1 || tags[0] != "high-fee-pressure-cap" {
		t.Fatalf("unexpected cap tags: %+v", tags)
	}
}

func TestCapHighFeePressureStepUpAllowsStrongPressure(t *testing.T) {
	got, tags := capHighFeePressureStepUp(
		2000,
		2500,
		1200,
		0,
		false,
		4,
		10,
		false,
		[]string{"htlc-liquidity-hot", "surge+20%"},
	)
	if got != 2500 || len(tags) != 0 {
		t.Fatalf("expected strong liquidity pressure to bypass cap, got ppm=%d tags=%+v", got, tags)
	}

	got, tags = capHighFeePressureStepUp(
		2000,
		2500,
		1200,
		1,
		false,
		2,
		19,
		false,
		[]string{"htlc-liquidity-hot", "surge+20%", "rebal-fail-pressure"},
	)
	if got != 2500 || len(tags) != 0 {
		t.Fatalf("expected confirmed failed-rebalance pressure to bypass cap, got ppm=%d tags=%+v", got, tags)
	}
}

func TestCapHighFeePressureWindowUpBlocksNegativeProfitWithoutStrongPressure(t *testing.T) {
	got, tags := capHighFeePressureWindowUp(
		1710,
		1785,
		-1843,
		autofeeRecentChangeStats{UpPpm24h: 75},
		0,
		false,
		1,
		18,
		false,
		[]string{"htlc-forward-hot", "large-gap-step-boost"},
	)
	if got != 1710 {
		t.Fatalf("expected weak high-fee loss channel to hold local ppm, got %d", got)
	}
	if len(tags) != 1 || tags[0] != "high-fee-loss-noup" {
		t.Fatalf("unexpected tags: %+v", tags)
	}
}

func TestCapHighFeePressureWindowUpBlocksLossEvenWithStrongHTLCWhenGoodLiquidity(t *testing.T) {
	got, tags := capHighFeePressureWindowUp(
		1925,
		2010,
		-709,
		autofeeRecentChangeStats{},
		0,
		false,
		4,
		9,
		true,
		[]string{"htlc-liquidity-hot", "htlc-forward-hot", "negm+8%", "htlc-liq+5%"},
	)
	if got != 1925 {
		t.Fatalf("expected loss-making high-fee channel with good liquidity to hold local ppm, got %d", got)
	}
	if len(tags) != 1 || tags[0] != "high-fee-loss-noup" {
		t.Fatalf("unexpected tags: %+v", tags)
	}
}

func TestCapHighFeePressureWindowUpLimitsCumulativeHighFeeUps(t *testing.T) {
	got, tags := capHighFeePressureWindowUp(
		1710,
		1810,
		120,
		autofeeRecentChangeStats{UpPpm24h: 75},
		0,
		false,
		1,
		18,
		false,
		[]string{"htlc-forward-hot", "large-gap-step-boost"},
	)
	want := 1710 + (highFeePressureWindowMaxPpm - 75)
	if got != want {
		t.Fatalf("expected cumulative 24h cap at %d, got %d", want, got)
	}
	if len(tags) != 1 || tags[0] != "high-fee-24h-cap" {
		t.Fatalf("unexpected tags: %+v", tags)
	}
}

func TestCapHighFeePressureWindowUpTreatsWeakRebalanceAttemptAsGradual(t *testing.T) {
	got, tags := capHighFeePressureWindowUp(
		1903,
		2226,
		2566,
		autofeeRecentChangeStats{},
		2,
		false,
		0,
		29,
		false,
		[]string{"rebal-attempt", "htlc-forward-hot", "surge+16%"},
	)
	want := 1903 + highFeePressureWindowMaxPpm
	if got != want {
		t.Fatalf("expected weak high-fee rebalance attempt to respect 24h cap at %d, got %d", want, got)
	}
	if len(tags) != 1 || tags[0] != "high-fee-24h-cap" {
		t.Fatalf("unexpected tags: %+v", tags)
	}
}

func TestCapHighFeePressureWindowUpAllowsStrongPressure(t *testing.T) {
	got, tags := capHighFeePressureWindowUp(
		1710,
		1810,
		-1843,
		autofeeRecentChangeStats{UpPpm24h: 150},
		0,
		false,
		4,
		10,
		false,
		[]string{"htlc-liquidity-hot", "htlc-forward-hot"},
	)
	if got != 1810 || len(tags) != 0 {
		t.Fatalf("expected strong liquidity pressure to bypass 24h cap, got ppm=%d tags=%+v", got, tags)
	}

	got, tags = capHighFeePressureWindowUp(
		1710,
		1810,
		-1843,
		autofeeRecentChangeStats{UpPpm24h: 150},
		2,
		false,
		1,
		18,
		false,
		[]string{"rebal-attempt", "htlc-forward-hot", "rebal-fail-pressure"},
	)
	if got != 1810 || len(tags) != 0 {
		t.Fatalf("expected confirmed failed-rebalance pressure to bypass 24h cap, got ppm=%d tags=%+v", got, tags)
	}
}

func TestCapGoodLiquidityDetachedOutrateUpLimitsHTLCOnlyJump(t *testing.T) {
	got, tags := capGoodLiquidityDetachedOutrateUp(
		366,
		412,
		313,
		1000,
		0.01,
		true,
		false,
		[]string{"htlc-liquidity-hot", "htlc-forward-hot", "trend-up"},
	)
	if got != 366 {
		t.Fatalf("expected good-liquidity htlc-only jump above outrate headroom to hold at local ppm, got %d", got)
	}
	if len(tags) != 1 || tags[0] != "goodliq-outrate-upcap" {
		t.Fatalf("unexpected tags: %+v", tags)
	}

	got, tags = capGoodLiquidityDetachedOutrateUp(
		704,
		823,
		704,
		1000,
		0.01,
		true,
		false,
		[]string{"htlc-liquidity-hot", "htlc-forward-hot", "trend-up"},
	)
	want := int(math.Ceil(704 * goodLiquidityOutrateUpCapMult))
	if got != want {
		t.Fatalf("expected good-liquidity jump to cap at outrate headroom %d, got %d", want, got)
	}
	if len(tags) != 1 || tags[0] != "goodliq-outrate-upcap" {
		t.Fatalf("unexpected tags: %+v", tags)
	}
}

func TestCapGoodLiquidityDetachedOutrateUpUsesTighterCapOnLowRevenue(t *testing.T) {
	got, tags := capGoodLiquidityDetachedOutrateUp(
		313,
		392,
		313,
		116,
		0.002,
		true,
		false,
		[]string{"rescue", "htlc-liquidity-hot", "htlc-forward-hot", "trend-up"},
	)
	want := int(math.Ceil(313 * goodLiquidityLowRevOutrateUpCapMult))
	if got != want {
		t.Fatalf("expected low-revenue good-liquidity jump to cap at tighter outrate headroom %d, got %d", want, got)
	}
	if !containsTag(tags, "goodliq-outrate-upcap") || !containsTag(tags, "goodliq-lowrev-upcap") {
		t.Fatalf("unexpected tags: %+v", tags)
	}
}

func TestCapGoodLiquidityDetachedOutrateUpBypassesOnConfirmedRebalanceFail(t *testing.T) {
	got, tags := capGoodLiquidityDetachedOutrateUp(
		366,
		412,
		313,
		-100,
		0,
		true,
		false,
		[]string{"htlc-liquidity-hot", "rebal-fail-pressure"},
	)
	if got != 412 || len(tags) != 0 {
		t.Fatalf("expected confirmed rebalance-fail pressure to bypass good-liquidity outrate cap, got ppm=%d tags=%+v", got, tags)
	}
}

func TestShouldHoldSeedNoSignalDownOnLowLiquidity(t *testing.T) {
	if !shouldHoldSeedNoSignalDownOnLowLiquidity(
		false,
		1611,
		1482,
		0.12,
		0.20,
		0,
		0,
		0,
		0,
		0,
		false,
		false,
		false,
		"seed",
		"rescue",
		[]string{"rescue", "rescue-floor-relax"},
	) {
		t.Fatalf("expected low-liquidity seed/rescue down with no local signals to hold")
	}

	if shouldHoldSeedNoSignalDownOnLowLiquidity(
		false,
		323,
		307,
		0.41,
		0.20,
		0,
		0,
		0,
		0,
		0,
		false,
		false,
		false,
		"seed",
		"seed",
		nil,
	) {
		t.Fatalf("did not expect good-liquidity seed down to hold")
	}

	if shouldHoldSeedNoSignalDownOnLowLiquidity(
		false,
		1502,
		1382,
		0.12,
		0.20,
		1653,
		1275,
		0,
		0,
		0,
		false,
		false,
		false,
		"seed",
		"rebal",
		nil,
	) {
		t.Fatalf("did not expect local out/rebal signals to be blocked by low-liquidity seed guard")
	}

	if shouldHoldSeedNoSignalDownOnLowLiquidity(
		false,
		500,
		460,
		0.08,
		0.20,
		0,
		0,
		0,
		0,
		0,
		false,
		true,
		false,
		"seed",
		"seed",
		[]string{"htlc-forward-hot"},
	) {
		t.Fatalf("did not expect HTLC pressure to be blocked by low-liquidity seed guard")
	}
}

func TestBuildAutofeeChannelLogEntryIncludesCostBasisAndProfit(t *testing.T) {
	d := &decision{
		Alias:          "lnmarkets.com",
		ChannelID:      42,
		LocalPpm:       1903,
		NewPpm:         2226,
		Target:         2226,
		TargetRaw:      2226,
		TargetFinal:    2226,
		Floor:          1900,
		Margin:         -249,
		ProfitFee7dSat: 3517,
		Tags:           []string{"neg-margin", "profit-positive-neg-margin-soft"},
	}

	entry := buildAutofeeChannelLogEntry(d, "changed", false, nil)
	if entry.Payload == nil {
		t.Fatalf("expected payload")
	}
	if entry.Payload.Margin != -249 || entry.Payload.CostBasisMarginPpm != -249 || entry.Payload.ProfitFee7dSat != 3517 {
		t.Fatalf("unexpected margin/profit payload: %+v", entry.Payload)
	}
	if !strings.Contains(entry.Line, "cost_marg_ppm=-249") || !strings.Contains(entry.Line, "profit7d_sat=3517") {
		t.Fatalf("expected line to expose cost margin and 7d profit, got %q", entry.Line)
	}
	if !strings.Contains(formatAutofeeTags(d), "profit-pos-neg-soft") {
		t.Fatalf("expected formatted softening tag, got %q", formatAutofeeTags(d))
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

func TestNormalizeAutofeeProfile(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty defaults", raw: "", want: autofeeProfileDefault},
		{name: "custom persists", raw: " custom ", want: autofeeProfileCustom},
		{name: "moderate", raw: "Moderate", want: "moderate"},
		{name: "aggressive", raw: "aggressive", want: "aggressive"},
		{name: "balanced alias", raw: "balanced", want: autofeeProfileDefault},
		{name: "unknown defaults", raw: "fast", want: autofeeProfileDefault},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeAutofeeProfile(tt.raw); got != tt.want {
				t.Fatalf("normalizeAutofeeProfile(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestEffectiveAutofeeProfileName(t *testing.T) {
	if got := effectiveAutofeeProfileName(autofeeProfileCustom); got != autofeeProfileDefault {
		t.Fatalf("custom effective profile = %q, want %q", got, autofeeProfileDefault)
	}
	if got := effectiveAutofeeProfileName("aggressive"); got != "aggressive" {
		t.Fatalf("aggressive effective profile = %q, want aggressive", got)
	}
}

func TestNewAutofeeEngineCustomUsesDefaultProfile(t *testing.T) {
	engine := newAutofeeEngine(nil, AutofeeConfig{Profile: autofeeProfileCustom})
	if engine.profile.Name != autofeeProfileDefault {
		t.Fatalf("custom engine profile = %q, want %q", engine.profile.Name, autofeeProfileDefault)
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

func TestApplySeedSignalCapsUsesOutrateMemory(t *testing.T) {
	profile := autofeeProfiles["moderate"]

	seed, tags := applySeedSignalCapsWithSources(profile, 815, 261, "outrate-mem", 0, "", 0)
	want := 261.0 * profile.SeedOutrateCapMult
	if math.Abs(seed-want) > 0.001 {
		t.Fatalf("expected mature seed to be capped by outrate memory: got %.1f want %.1f", seed, want)
	}
	if !containsTag(tags, "seed:outmemcap") {
		t.Fatalf("expected seed:outmemcap tag, got %+v", tags)
	}
}

func TestShouldMuteHTLCForwardHotUpwardPressure(t *testing.T) {
	if !shouldMuteHTLCForwardHotUpwardPressure(false, true, false, false, 0.88, 0.30, false, false, 0, 0) {
		t.Fatalf("expected HTLC forward-only pressure to be muted on good-liquidity channel without local signals")
	}
	if shouldMuteHTLCForwardHotUpwardPressure(false, true, false, false, 0.88, 0.30, true, false, 0, 0) {
		t.Fatalf("did not expect mute when observed outbound flow exists")
	}
	if shouldMuteHTLCForwardHotUpwardPressure(false, true, false, false, 0.12, 0.30, false, false, 0, 0) {
		t.Fatalf("did not expect mute on low-liquidity channel")
	}
	if shouldMuteHTLCForwardHotUpwardPressure(false, true, true, false, 0.88, 0.30, false, false, 0, 0) {
		t.Fatalf("did not expect mute when policy/liquidity HTLC pressure exists")
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

	active, tags := manageRescueState(st, now, true, ranking, true, 1008, 637, 1202, 0.003, false, false)
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
	active, tags = manageRescueState(st, now.Add(13*time.Hour), true, recovered, true, 740, 700, 700, 0.001, false, false)
	if !active || st.ExplorerState.RescueRecoverRounds != 1 {
		t.Fatalf("expected rescue exit to require confirmation, active=%v state=%+v tags=%+v", active, st.ExplorerState, tags)
	}
	active, tags = manageRescueState(st, now.Add(14*time.Hour), true, recovered, true, 740, 700, 700, 0.001, false, false)
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

	active, tags := manageRescueState(st, now, true, ranking, true, 1263, 640, 1202, 0.003, false, false)
	if !active {
		t.Fatalf("expected high-priority rescue candidate to bypass reentry cooldown")
	}
	if !containsTag(tags, "rescue-enter") || !st.ExplorerState.RescueActive {
		t.Fatalf("unexpected priority rescue entry state: tags=%+v state=%+v", tags, st.ExplorerState)
	}
}

func TestManageRescueStateSkipsSlowCycleProtectedChannel(t *testing.T) {
	st := &autofeeChannelState{}
	now := time.Unix(1_700_000_000, 0).UTC()
	ranking := autofeeRankingSnapshot{
		Score:          5,
		State:          "close",
		TrendDirection: "worsening",
		ProfitFee7dSat: -14091,
	}

	active, tags := manageRescueState(st, now, true, ranking, true, 1263, 640, 1202, 0.003, false, true)
	if active {
		t.Fatalf("expected rescue to stay disabled for slow-cycle protected channel")
	}
	if len(tags) != 0 || st.ExplorerState.RescueActive {
		t.Fatalf("unexpected rescue activity for slow-cycle protected channel: tags=%+v state=%+v", tags, st.ExplorerState)
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

func TestApplySlowCycle30dReferencesModerateDrained(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	ranking := autofeeRankingSnapshot{
		Score30d:           58,
		ProfitFee7dSat:     -14091,
		ProfitFee30dSat:    1722,
		OutPpm30d:          2587,
		RebalPpm30d:        2608,
		PeerStabilityScore: 52,
	}

	outRef, rebalRef, active, tags := applySlowCycle30dReferences(profile, ranking, true, 0.03, 280, 2616)
	if !active {
		t.Fatalf("expected slow-cycle 30d protection to activate")
	}
	if outRef != 1203 {
		t.Fatalf("unexpected slow-cycle 30d outrate ref: got %d want 1203", outRef)
	}
	if rebalRef != 2616 {
		t.Fatalf("expected existing 7d rebal cost to remain the hard floor, got %d want 2616", rebalRef)
	}
	if !containsTag(tags, "slow-cycle-30d") || !containsTag(tags, "slow-cycle-30d-out") {
		t.Fatalf("expected slow-cycle 30d tags, got %+v", tags)
	}
}

func TestApplySlowCycle30dReferencesRejectsWeakThirtyDayCase(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	ranking := autofeeRankingSnapshot{
		Score30d:           11,
		ProfitFee7dSat:     -14948,
		ProfitFee30dSat:    -3218,
		OutPpm30d:          2134,
		RebalPpm30d:        2531,
		PeerStabilityScore: 51,
	}

	outRef, rebalRef, active, tags := applySlowCycle30dReferences(profile, ranking, true, 0.10, 235, 2609)
	if active {
		t.Fatalf("did not expect slow-cycle 30d protection for structurally weak 30d channel")
	}
	if outRef != 235 || rebalRef != 2609 || len(tags) != 0 {
		t.Fatalf("unexpected slow-cycle fallback output: out=%d rebal=%d tags=%+v", outRef, rebalRef, tags)
	}
}

func TestShouldUseAssistChannel(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	ranking := autofeeRankingSnapshot{
		State:                   "expand",
		ProfitFee7dSat:          120,
		AssistedForwardFee7dSat: 3200,
		ForwardInCount7d:        18,
		ForwardInAmountSat7d:    2_400_000,
		ForwardOutCount7d:       4,
		ForwardOutAmountSat7d:   1_900_000,
		RebalanceDependence:     15,
	}

	if !shouldUseAssistChannel(profile, ranking, true, "router", 0) {
		t.Fatalf("expected assisted low-fee router to be classified as assist-channel")
	}
	if shouldUseAssistChannel(profile, ranking, true, "sink", 0) {
		t.Fatalf("did not expect sink channel to be classified as assist-channel")
	}
	if shouldUseAssistChannel(profile, ranking, true, "router", 400) {
		t.Fatalf("did not expect high-fee channel to be classified as assist-channel")
	}
}

func TestShouldPreserveAssistChannelUpwardPressure(t *testing.T) {
	if !shouldPreserveAssistChannelUpwardPressure(true, 0, false) {
		t.Fatalf("expected assist-channel to preserve low fee without hard upward signal")
	}
	if shouldPreserveAssistChannelUpwardPressure(true, 1, false) {
		t.Fatalf("did not expect assist preserve during recent rebalance pressure")
	}
	if shouldPreserveAssistChannelUpwardPressure(true, 0, true) {
		t.Fatalf("did not expect assist preserve during htlc liquidity pressure")
	}
}

func TestDeriveRankingPolicyRestrictsWeakCloseWithoutHardSignal(t *testing.T) {
	ranking := autofeeRankingSnapshot{
		State:           "close",
		TrendDirection:  "worsening",
		ProfitFee7dSat:  -1200,
		ProfitFee30dSat: -900,
		Score:           24,
		Score30d:        41,
	}

	tags, restrict := deriveRankingPolicy(ranking, true, 0, false, false)
	if !restrict {
		t.Fatalf("expected weak close channel to restrict upward pressure")
	}
	if !containsTag(tags, "rank-close") {
		t.Fatalf("expected rank-close tag, got %+v", tags)
	}
}

func TestDeriveRankingPolicyBypassesRestrictionOnHardSignal(t *testing.T) {
	ranking := autofeeRankingSnapshot{
		State:           "close",
		TrendDirection:  "worsening",
		ProfitFee7dSat:  -1200,
		ProfitFee30dSat: -900,
		Score:           24,
		Score30d:        41,
	}

	_, restrict := deriveRankingPolicy(ranking, true, 1, false, false)
	if restrict {
		t.Fatalf("did not expect upward restriction when recent rebalance exists")
	}
}

func TestDeriveRankingPolicyRestrictsMonitorRebalanceLossWithoutWorsening(t *testing.T) {
	ranking := autofeeRankingSnapshot{
		State:               "monitor",
		TrendDirection:      "stable",
		ProfitFee7dSat:      -1200,
		ProfitFee30dSat:     900,
		Score:               62,
		Score30d:            72,
		OutPpm30d:           1200,
		RebalPpm30d:         1280,
		RebalanceDependence: 55,
	}

	tags, restrict := deriveRankingPolicy(ranking, true, 0, false, false)
	if !restrict {
		t.Fatalf("expected monitor channel with rebalance-heavy loss to restrict upward pressure")
	}
	if !containsTag(tags, "rank-monitor") || !containsTag(tags, "rank-monitor-loss-noup") {
		t.Fatalf("expected monitor loss restriction tags, got %+v", tags)
	}
}

func TestDeriveRankingPolicyAllowsMonitorRebalanceLossOnHardSignal(t *testing.T) {
	ranking := autofeeRankingSnapshot{
		State:               "monitor",
		TrendDirection:      "stable",
		ProfitFee7dSat:      -1200,
		ProfitFee30dSat:     900,
		Score:               62,
		Score30d:            72,
		OutPpm30d:           1200,
		RebalPpm30d:         1280,
		RebalanceDependence: 55,
	}

	_, restrict := deriveRankingPolicy(ranking, true, 1, false, false)
	if restrict {
		t.Fatalf("did not expect upward restriction when recent rebalance confirms demand")
	}
}

func TestDeriveRankingPolicyAllowsMonitorLossWithoutRebalancePressure(t *testing.T) {
	ranking := autofeeRankingSnapshot{
		State:               "monitor",
		TrendDirection:      "stable",
		ProfitFee7dSat:      -120,
		ProfitFee30dSat:     900,
		Score:               62,
		Score30d:            72,
		OutPpm30d:           1200,
		RebalPpm30d:         420,
		RebalanceDependence: 20,
	}

	_, restrict := deriveRankingPolicy(ranking, true, 0, false, false)
	if restrict {
		t.Fatalf("did not expect upward restriction for a light, non-rebalance-heavy monitor loss")
	}
}

func TestDeriveRankingPolicyDoesNotRestrictExpand(t *testing.T) {
	ranking := autofeeRankingSnapshot{
		State:           "expand",
		TrendDirection:  "stable",
		ProfitFee7dSat:  1200,
		ProfitFee30dSat: 2400,
		Score:           78,
		Score30d:        82,
	}

	tags, restrict := deriveRankingPolicy(ranking, true, 0, false, false)
	if restrict {
		t.Fatalf("did not expect expand channel to restrict upward pressure")
	}
	if !containsTag(tags, "rank-expand") {
		t.Fatalf("expected rank-expand tag, got %+v", tags)
	}
}

func TestDeriveRebalanceExecutionPolicyDisabledSink(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	runtime := autofeeRebalanceRuntimeSnapshot{
		AutoEnabled:          false,
		ManualRestartEnabled: false,
	}

	tags, restrict, anchor, active := deriveRebalanceExecutionPolicy(
		profile,
		runtime,
		true,
		1.10,
		"",
		"sink",
		240,
		defaultBootstrapHours,
		0.04,
		0.10,
		700,
		850,
		850,
		"rebal",
		0,
		0,
		false,
		false,
		1200,
	)
	if !restrict || !active {
		t.Fatalf("expected disabled sink to activate rebalance execution guard")
	}
	if anchor >= 1200 || anchor <= 0 {
		t.Fatalf("expected a valid downward anchor, got %d", anchor)
	}
	if !containsTag(tags, "rebal-exec-disabled") {
		t.Fatalf("expected rebal-exec-disabled tag, got %+v", tags)
	}
}

func TestDeriveRebalanceExecutionPolicyBudgetBlockedTarget(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	runtime := autofeeRebalanceRuntimeSnapshot{
		AutoEnabled:      true,
		EligibleAsTarget: true,
	}

	tags, restrict, anchor, active := deriveRebalanceExecutionPolicy(
		profile,
		runtime,
		true,
		1.10,
		"budget_exhausted",
		"sink",
		240,
		defaultBootstrapHours,
		0.04,
		0.10,
		650,
		900,
		900,
		"rebal",
		0,
		0,
		false,
		false,
		1300,
	)
	if !restrict {
		t.Fatalf("expected budget-blocked sink to restrict upward pressure")
	}
	if active || anchor != 0 {
		t.Fatalf("budget pressure must not create a down anchor, active=%v anchor=%d", active, anchor)
	}
	if !containsTag(tags, "rebal-exec-budget") {
		t.Fatalf("expected rebal-exec-budget tag, got %+v", tags)
	}
}

func TestDeriveRebalanceExecutionPolicySkipsOnHardSignal(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	runtime := autofeeRebalanceRuntimeSnapshot{
		AutoEnabled:      true,
		EligibleAsTarget: true,
	}

	_, restrict, _, active := deriveRebalanceExecutionPolicy(
		profile,
		runtime,
		true,
		1.10,
		"budget_exhausted",
		"sink",
		240,
		defaultBootstrapHours,
		0.04,
		0.10,
		650,
		900,
		900,
		"rebal",
		1,
		0,
		false,
		false,
		1300,
	)
	if restrict || active {
		t.Fatalf("did not expect rebalance execution guard with recent rebalance signal")
	}
}

func TestShouldHoldForAutofeeChurnBlocksRepeatedUps(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	recent := autofeeRecentChangeStats{
		ChangeCount24h:   2,
		UpCount24h:       2,
		ConsecutiveUp24h: 2,
	}

	tags := shouldHoldForAutofeeChurn(profile, recent, 500, 560, 0, false)
	if !containsTag(tags, "churn-up-lock") {
		t.Fatalf("expected churn-up-lock, got %+v", tags)
	}
}

func TestShouldHoldForAutofeeChurnBlocksBusyDayWithoutHardSignal(t *testing.T) {
	profile := autofeeProfiles["conservative"]
	recent := autofeeRecentChangeStats{
		ChangeCount24h:   2,
		UpCount24h:       1,
		DownCount24h:     1,
		ConsecutiveUp24h: 0,
	}

	tags := shouldHoldForAutofeeChurn(profile, recent, 500, 540, 0, false)
	if !containsTag(tags, "churn-24h-lock") {
		t.Fatalf("expected churn-24h-lock, got %+v", tags)
	}
}

func TestShouldHoldForAutofeeChurnBypassesOnHardSignal(t *testing.T) {
	profile := autofeeProfiles["moderate"]
	recent := autofeeRecentChangeStats{
		ChangeCount24h:   4,
		UpCount24h:       3,
		ConsecutiveUp24h: 3,
	}

	tags := shouldHoldForAutofeeChurn(profile, recent, 500, 560, 1, false)
	if len(tags) != 0 {
		t.Fatalf("did not expect churn lock with recent rebalance hard signal, got %+v", tags)
	}
}

func TestShouldHoldAutofeeForRebalanceSettling(t *testing.T) {
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	hard := recentRebalanceSignal{Count: 1, LastAt: now.Add(-10 * time.Minute)}
	if !shouldHoldAutofeeForRebalanceSettling(hard, now, autofeeRebalanceSettlingWindow) {
		t.Fatalf("expected recent successful rebalance to hold autofee apply")
	}

	weak := recentRebalanceSignal{WeakCount: 1, WeakLastAt: now.Add(-10 * time.Minute)}
	if !shouldHoldAutofeeForRebalanceSettling(weak, now, autofeeRebalanceSettlingWindow) {
		t.Fatalf("expected recent weak rebalance attempt to hold autofee apply")
	}

	old := recentRebalanceSignal{Count: 1, LastAt: now.Add(-2 * time.Hour)}
	if shouldHoldAutofeeForRebalanceSettling(old, now, autofeeRebalanceSettlingWindow) {
		t.Fatalf("did not expect old rebalance signal to hold autofee apply")
	}
}

func TestAutofeeSettlingSkipLogEntry(t *testing.T) {
	d := &decision{
		Alias:    "peer",
		Apply:    false,
		LocalPpm: 100,
		NewPpm:   140,
		Tags:     []string{"autofee_settling"},
	}
	_, category := formatAutofeeDecisionLine(d, false, false)
	if category != "skipped" {
		t.Fatalf("expected settling decision category skipped, got %q", category)
	}
	entry := buildAutofeeChannelLogEntry(d, "", false, nil)
	if entry.Payload == nil || entry.Payload.SkipReason != "autofee_settling" {
		t.Fatalf("expected skip_reason=autofee_settling, got %+v", entry.Payload)
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

func TestIsTransientApplyError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"context_canceled_sentinel", context.Canceled, false},
		{"context_deadline_sentinel", context.DeadlineExceeded, false},
		{"grpc_unavailable", errors.New("rpc error: code = Unavailable desc = connection error"), true},
		{"grpc_deadline_string", errors.New("context deadline exceeded while waiting"), true},
		{"resource_exhausted", errors.New("rpc error: code = ResourceExhausted desc = ..."), true},
		{"transport_closing", errors.New("transport is closing"), true},
		{"connection_reset", errors.New("read tcp: connection reset by peer"), true},
		{"connection_refused", errors.New("dial tcp: connect: connection refused"), true},
		{"i_o_timeout", errors.New("read tcp: i/o timeout"), true},
		{"eof", errors.New("EOF"), true},
		{"invalid_argument", errors.New("rpc error: code = InvalidArgument desc = bad fee"), false},
		{"not_found", errors.New("channel not found"), false},
		{"permission_denied", errors.New("permission denied"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransientApplyError(tc.err); got != tc.want {
				t.Fatalf("isTransientApplyError(%v) = %v want %v", tc.err, got, tc.want)
			}
		})
	}
}
