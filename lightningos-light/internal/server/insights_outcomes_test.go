package server

import (
	"math"
	"testing"
)

func TestInsightOutcomeVerdictFollowed(t *testing.T) {
	before := insightWindowMetrics{DirectFeeSat: 700, AssistedFeeSat: 70, RebalCostSat: 350}
	// Net went from 420 to 3220 over the week: +400/day against 500 predicted.
	after := insightWindowMetrics{DirectFeeSat: 3500, AssistedFeeSat: 70, RebalCostSat: 350}
	verdict, realized, errPct := insightOutcomeVerdict(insightKindRefillSeller, insightOutcomeDecisionDone, 500, before, after, 7)
	if verdict != insightVerdictHit || realized != 400 {
		t.Fatalf("expected hit at 400/day, got %s %d", verdict, realized)
	}
	if errPct == nil || math.Abs(*errPct-20) > 0.01 {
		t.Fatalf("expected 20%% error against 500 predicted, got %v", errPct)
	}

	// Less than half of the prediction: neutral.
	half := insightWindowMetrics{DirectFeeSat: 1500, AssistedFeeSat: 70, RebalCostSat: 350}
	if verdict, realized, _ := insightOutcomeVerdict(insightKindRefillSeller, insightOutcomeDecisionDone, 500, before, half, 7); verdict != insightVerdictNeutral || realized != 114 {
		t.Fatalf("expected neutral at 114/day, got %s %d", verdict, realized)
	}

	// Worse than before: miss.
	worse := insightWindowMetrics{DirectFeeSat: 300, AssistedFeeSat: 20, RebalCostSat: 600}
	if verdict, _, _ := insightOutcomeVerdict(insightKindPriceOff, insightOutcomeDecisionDone, 200, before, worse, 7); verdict != insightVerdictMiss {
		t.Fatalf("expected miss, got %s", verdict)
	}
}

func TestInsightOutcomeVerdictStopBuying(t *testing.T) {
	before := insightWindowMetrics{DirectFeeSat: 50, RebalCostSat: 2100} // 300/day spent
	stopped := insightWindowMetrics{DirectFeeSat: 60, RebalCostSat: 350} // 50/day
	verdict, realized, errPct := insightOutcomeVerdict(insightKindStopBuying, insightOutcomeDecisionDone, 300, before, stopped, 7)
	if verdict != insightVerdictHit || realized != 250 {
		t.Fatalf("spending that fell from 300 to 50 a day is a hit of 250, got %s %d", verdict, realized)
	}
	if errPct == nil || math.Abs(*errPct-16.7) > 0.05 {
		t.Fatalf("expected 16.7%% error, got %v", errPct)
	}
	kept := insightWindowMetrics{DirectFeeSat: 40, RebalCostSat: 2300}
	if verdict, _, _ := insightOutcomeVerdict(insightKindStopBuying, insightOutcomeDecisionDone, 300, before, kept, 7); verdict != insightVerdictMiss {
		t.Fatalf("spending that kept going is a miss, got %s", verdict)
	}
	// Dismissed and the spending kept going: the recommendation stood.
	if verdict, _, errPct := insightOutcomeVerdict(insightKindStopBuying, insightOutcomeDecisionSkip, 300, before, kept, 7); verdict != insightVerdictConfirmed || errPct != nil {
		t.Fatalf("dismissed + spending continued must be confirmed without error pct, got %s %v", verdict, errPct)
	}
}

func TestInsightOutcomeVerdictDismissed(t *testing.T) {
	before := insightWindowMetrics{DirectFeeSat: 700}
	improved := insightWindowMetrics{DirectFeeSat: 4200}
	if verdict, _, _ := insightOutcomeVerdict(insightKindRefillSeller, insightOutcomeDecisionSkip, 500, before, improved, 7); verdict != insightVerdictUnneeded {
		t.Fatalf("channel that improved by itself makes the dismissal right: unneeded, got %s", verdict)
	}
	flat := insightWindowMetrics{DirectFeeSat: 710}
	if verdict, _, _ := insightOutcomeVerdict(insightKindRefillSeller, insightOutcomeDecisionSkip, 500, before, flat, 7); verdict != insightVerdictConfirmed {
		t.Fatalf("channel that stayed flat confirms the recommendation, got %s", verdict)
	}
}

func TestInsightRuleWeightNeedsSampleThenScales(t *testing.T) {
	if w, applied := insightRuleWeight(InsightRuleScore{Measured: 4, Hits: 4, Accuracy: 1}); applied || w != 1 {
		t.Fatalf("four measurements must not weigh yet, got %v %v", w, applied)
	}
	if w, applied := insightRuleWeight(InsightRuleScore{Measured: 5, Accuracy: 0.5}); !applied || w != 1 {
		t.Fatalf("50%% accuracy is neutral, got %v %v", w, applied)
	}
	if w, _ := insightRuleWeight(InsightRuleScore{Measured: 8, Accuracy: 0}); w != insightRuleWeightMin {
		t.Fatalf("a rule that never lands drops to %.1f, got %v", insightRuleWeightMin, w)
	}
	if w, _ := insightRuleWeight(InsightRuleScore{Measured: 8, Accuracy: 1}); w != insightRuleWeightMax {
		t.Fatalf("a rule that always lands rises to %.1f, got %v", insightRuleWeightMax, w)
	}
	weights := insightRuleWeightsFromScorecard(InsightScorecard{Rules: []InsightRuleScore{
		{Rule: "R1", Weight: 1.3, WeightApplied: true},
		{Rule: "R2", Weight: 1, WeightApplied: false},
	}})
	if weights["R1"] != 1.3 {
		t.Fatalf("applied weight must be exported, got %v", weights)
	}
	if _, ok := weights["R2"]; ok {
		t.Fatalf("unapplied weight must stay absent, got %v", weights)
	}
	ins := Insight{Rule: "R1", Kind: insightKindRefillSeller, Confidence: insightConfidenceHigh, ValueSatDay: 1000}
	base := insightPriority(ins)
	if got := insightPriorityWeighted(ins, weights); got != math.Round(base*1.3) {
		t.Fatalf("weighted priority = base × weight, got %v (base %v)", got, base)
	}
	if got := insightPriorityWeighted(Insight{Rule: "R2", Kind: insightKindMoreCapacity}, weights); got != insightPriority(Insight{Rule: "R2", Kind: insightKindMoreCapacity}) {
		t.Fatalf("rules without a weight keep the designed priority, got %v", got)
	}
}
