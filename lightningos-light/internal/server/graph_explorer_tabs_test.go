package server

import (
	"testing"
	"time"
)

func TestNormalizeGraphExplorerCloseType(t *testing.T) {
	tests := map[string]string{
		"":                   "unknown",
		"COOPERATIVE_CLOSE":  "mutual_close",
		"mutual":             "mutual_close",
		"LOCAL_FORCE_CLOSE":  "force_close",
		"REMOTE_FORCE_CLOSE": "force_close",
		"BREACH_CLOSE":       "penalty_close",
		"PENALTY_CLOSE":      "penalty_close",
		"FUNDING_CANCELED":   "unknown",
		"ABANDONED":          "unknown",
		"something_custom":   "something_custom",
	}

	for input, expected := range tests {
		if got := normalizeGraphExplorerCloseType(input); got != expected {
			t.Fatalf("normalizeGraphExplorerCloseType(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestGraphExplorerShortChannelID(t *testing.T) {
	const channelID uint64 = 1036267719070122000
	if got, want := graphExplorerShortChannelID(channelID), "942480x1889x16"; got != want {
		t.Fatalf("graphExplorerShortChannelID(%d) = %q, want %q", channelID, got, want)
	}
}

func TestSummarizeGraphExplorerPoliciesComputesCorrectedAverage(t *testing.T) {
	samples := []graphExplorerPolicySample{
		{Ppm: 25, CapacitySat: 1_000_000},
		{Ppm: 25, CapacitySat: 1_000_000},
		{Ppm: 25, CapacitySat: 1_000_000},
		{Ppm: 3500, CapacitySat: 1_000_000},
	}

	summary := summarizeGraphExplorerPolicies(samples)
	if summary.WeightedAvgPpm != 893 {
		t.Fatalf("expected weighted avg 893, got %d", summary.WeightedAvgPpm)
	}
	if summary.CorrectedAvgPpm != 31 {
		t.Fatalf("expected corrected avg 31, got %d", summary.CorrectedAvgPpm)
	}
	if summary.CorrectedAvgPpm >= summary.WeightedAvgPpm {
		t.Fatalf("expected corrected avg %d to stay below weighted avg %d", summary.CorrectedAvgPpm, summary.WeightedAvgPpm)
	}
}

func TestGraphExplorerBuildFeeHistoryIncludesCorrectedAverage(t *testing.T) {
	day := time.Date(2026, time.April, 11, 0, 0, 0, 0, time.UTC)
	history := graphExplorerBuildFeeHistory(map[string]*graphExplorerFeeHistoryBucket{
		"2026-04-11": {
			Day: day,
			Outbound: []graphExplorerPolicySample{
				{Ppm: 10, CapacitySat: 1_000},
				{Ppm: 10, CapacitySat: 1_000},
				{Ppm: 1000, CapacitySat: 1_000},
			},
			Inbound: []graphExplorerPolicySample{
				{Ppm: -500, CapacitySat: 1_000},
				{Ppm: -500, CapacitySat: 1_000},
				{Ppm: 0, CapacitySat: 1_000},
			},
		},
	})

	if len(history) != 1 {
		t.Fatalf("expected 1 history item, got %d", len(history))
	}
	if history[0].Day != "2026-04-11" {
		t.Fatalf("expected day 2026-04-11, got %q", history[0].Day)
	}
	if history[0].OutboundCorrectedAvgPpm != 18 {
		t.Fatalf("expected outbound corrected avg 18, got %d", history[0].OutboundCorrectedAvgPpm)
	}
	if history[0].InboundCorrectedAvgPpm != -491 {
		t.Fatalf("expected inbound corrected avg -491, got %d", history[0].InboundCorrectedAvgPpm)
	}
}
