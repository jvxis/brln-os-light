package server

import (
	"testing"
	"time"

	"lightningos-light/internal/lndclient"
)

func TestValidateAutomationIntentConfigUpdate(t *testing.T) {
	validMode := automationIntentModeShadow
	validMultiplier := 1.2
	validConfidence := 0.7
	if err := validateAutomationIntentConfigUpdate(AutomationIntentConfigUpdate{
		Mode: &validMode, RefillScoreMultiplier: &validMultiplier, MinConfidence: &validConfidence,
	}); err != nil {
		t.Fatalf("expected valid config: %v", err)
	}

	invalidMode := "automatic"
	if err := validateAutomationIntentConfigUpdate(AutomationIntentConfigUpdate{Mode: &invalidMode}); err == nil {
		t.Fatal("expected invalid mode to fail")
	}
	invalidMultiplier := 1.6
	if err := validateAutomationIntentConfigUpdate(AutomationIntentConfigUpdate{RefillScoreMultiplier: &invalidMultiplier}); err == nil {
		t.Fatal("expected invalid multiplier to fail")
	}
}

func TestApplyRefillTargetIntentsHonorsModeProfileAndConfidence(t *testing.T) {
	intent := AutomationIntent{
		ID: 1, ChannelID: 42, Kind: automationIntentKindRefillTarget,
		Confidence: 0.8, ReasonCode: "test",
	}
	makeCandidates := func() []rebalanceTarget {
		return []rebalanceTarget{{Channel: RebalanceChannel{ChannelID: 42}, Score: 1000}}
	}

	shadow := makeCandidates()
	count := applyRefillTargetIntents(shadow, map[uint64][]AutomationIntent{42: {intent}}, AutomationIntentConfig{
		Mode: automationIntentModeShadow, RefillScoreMultiplier: 1.2, MinConfidence: 0.7,
	}, rebalanceProfileBalanced)
	if count != 1 || shadow[0].Score != 1000 || !shadow[0].IntentShadow || shadow[0].IntentScoreAfter != 1160 {
		t.Fatalf("unexpected shadow result: count=%d target=%+v", count, shadow[0])
	}

	enforced := makeCandidates()
	applyRefillTargetIntents(enforced, map[uint64][]AutomationIntent{42: {intent}}, AutomationIntentConfig{
		Mode: automationIntentModeEnforce, RefillScoreMultiplier: 1.2, MinConfidence: 0.7,
	}, rebalanceProfileBalanced)
	if enforced[0].Score != 1160 || !enforced[0].IntentApplied {
		t.Fatalf("expected balanced enforce score 1160, got %+v", enforced[0])
	}

	conservative := makeCandidates()
	applyRefillTargetIntents(conservative, map[uint64][]AutomationIntent{42: {intent}}, AutomationIntentConfig{
		Mode: automationIntentModeEnforce, RefillScoreMultiplier: 1.2, MinConfidence: 0.7,
	}, rebalanceProfileConservative)
	if conservative[0].Score != 1080 {
		t.Fatalf("expected conservative score 1080, got %d", conservative[0].Score)
	}
	negative := []rebalanceTarget{{Channel: RebalanceChannel{ChannelID: 42}, Score: -1000}}
	applyRefillTargetIntents(negative, map[uint64][]AutomationIntent{42: {intent}}, AutomationIntentConfig{
		Mode: automationIntentModeEnforce, RefillScoreMultiplier: 1.2, MinConfidence: 0.7,
	}, rebalanceProfileBalanced)
	if negative[0].Score != -862 {
		t.Fatalf("expected negative score to move toward zero, got %d", negative[0].Score)
	}

	lowConfidence := makeCandidates()
	intent.Confidence = 0.6
	if got := applyRefillTargetIntents(lowConfidence, map[uint64][]AutomationIntent{42: {intent}}, AutomationIntentConfig{
		Mode: automationIntentModeEnforce, RefillScoreMultiplier: 1.2, MinConfidence: 0.7,
	}, rebalanceProfileAggressive); got != 0 || lowConfidence[0].Score != 1000 {
		t.Fatalf("low-confidence intent must not apply: count=%d score=%d", got, lowConfidence[0].Score)
	}
}

func TestApplyProtectFeeFloorIntentIsDirectional(t *testing.T) {
	intent := AutomationIntent{ID: 7, Kind: automationIntentKindProtectFeeFloor, Confidence: 0.9, FeeFloorPPM: 400}
	cfg := AutomationIntentConfig{Mode: automationIntentModeEnforce, MinConfidence: 0.7}

	ppm, apply, selected, shadow := applyProtectFeeFloorIntent(500, 300, true, []AutomationIntent{intent}, cfg, 1, 2000)
	if ppm != 400 || !apply || selected == nil || shadow {
		t.Fatalf("expected downward decision capped at 400: ppm=%d apply=%v selected=%v shadow=%v", ppm, apply, selected, shadow)
	}
	ppm, apply, selected, _ = applyProtectFeeFloorIntent(350, 300, true, []AutomationIntent{intent}, cfg, 1, 2000)
	if ppm != 350 || apply || selected == nil {
		t.Fatalf("floor above local fee should hold current fee: ppm=%d apply=%v selected=%v", ppm, apply, selected)
	}
	ppm, apply, selected, _ = applyProtectFeeFloorIntent(300, 450, true, []AutomationIntent{intent}, cfg, 1, 2000)
	if ppm != 450 || !apply || selected != nil {
		t.Fatalf("upward decision must remain untouched: ppm=%d apply=%v selected=%v", ppm, apply, selected)
	}

	cfg.Mode = automationIntentModeShadow
	ppm, apply, selected, shadow = applyProtectFeeFloorIntent(500, 300, true, []AutomationIntent{intent}, cfg, 1, 2000)
	if ppm != 400 || !apply || selected == nil || !shadow {
		t.Fatalf("shadow must return the hypothetical effect without applying it: ppm=%d apply=%v selected=%v shadow=%v", ppm, apply, selected, shadow)
	}
}

func TestDeriveRefillTargetIntentUsesAutofeeCalibrationAndRebalanceEligibility(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	engine := &autofeeEngine{
		profile: autofeeProfile{Name: "moderate"},
		calib:   autofeeCalibration{NodeClass: "M", LiquidityClass: "balanced"},
		now:     now,
		automationIntentConfig: AutomationIntentConfig{
			Mode: automationIntentModeShadow, RefillTargetTTLSec: 21600,
			RefillScoreMultiplier: 1.2, MinConfidence: 0.7,
		},
		rebalanceRuntime: map[uint64]autofeeRebalanceRuntimeSnapshot{
			42: {AutoEnabled: true, EligibleAsTarget: true, ROIEstimate: 1.4, ROIEstimateValid: true},
		},
	}
	channel := lndclient.ChannelInfo{ChannelID: 42, ChannelPoint: "tx:0", Active: true}
	decision := &decision{ChannelID: 42, LiquidityState: "extreme-drained", OutRatio: 0.01, LocalPpm: 500, Target: 650, NewPpm: 600, FwdCount: 3}
	intent, ok := engine.deriveRefillTargetIntent(channel, decision, "run-1")
	if !ok {
		t.Fatal("expected eligible extreme-drained channel to publish intent")
	}
	if intent.ProducerProfile != "moderate" || intent.ProducerNodeClass != "M" || intent.ProducerLiquidityClass != "balanced" {
		t.Fatalf("producer calibration provenance missing: %+v", intent)
	}
	if intent.ExpiresAt.Sub(now) != 6*time.Hour {
		t.Fatalf("unexpected ttl: %s", intent.ExpiresAt.Sub(now))
	}

	engine.rebalanceRuntime[42] = autofeeRebalanceRuntimeSnapshot{AutoEnabled: true, EligibleAsTarget: false}
	if _, ok := engine.deriveRefillTargetIntent(channel, decision, "run-2"); ok {
		t.Fatal("intent must not bypass rebalance eligibility")
	}
}
