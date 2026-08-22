package server

import "testing"

func TestSovereignShadowIsPureDryRun(t *testing.T) {
	if !isSovereignSchedulerMode(rebalanceSchedulerModeSovereignShadow) {
		t.Fatal("shadow mode must be owned exclusively by the sovereign scheduler")
	}
	if shouldQueueGuaranteedRebalanceSlot(rebalanceSchedulerModeSovereignShadow) {
		t.Fatal("shadow mode must not queue a guaranteed rebalance slot")
	}
	if shouldEvaluateAutoTarget(rebalanceSchedulerModeSovereignShadow) {
		t.Fatal("shadow mode must not mutate channel targets")
	}
}

func TestLiveAndRulesSchedulerMutationPolicy(t *testing.T) {
	if !shouldQueueGuaranteedRebalanceSlot(rebalanceSchedulerModeSovereignLive) {
		t.Fatal("sovereign live mode must retain guaranteed-slot execution")
	}
	if !shouldEvaluateAutoTarget(rebalanceSchedulerModeSovereignLive) {
		t.Fatal("sovereign live mode must retain AutoTarget evaluation")
	}
	if !shouldQueueGuaranteedRebalanceSlot(rebalanceSchedulerModeRulesAuto) {
		t.Fatal("rules-auto mode must retain guaranteed-slot execution")
	}
	if shouldEvaluateAutoTarget(rebalanceSchedulerModeRulesAuto) {
		t.Fatal("rules-auto mode does not run sovereign AutoTarget evaluation")
	}
}
