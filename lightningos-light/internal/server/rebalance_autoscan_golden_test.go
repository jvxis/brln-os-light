package server

import (
	"reflect"
	"testing"
	"time"
)

func rebalanceGoldenSource(id uint64, maxSourceSat int64) RebalanceChannel {
	return RebalanceChannel{
		ChannelID:        id,
		ChannelPoint:     "source-point",
		PeerAlias:        "source",
		Active:           true,
		EligibleAsSource: true,
		MaxSourceSat:     maxSourceSat,
	}
}

func rebalanceGoldenTarget(id uint64, alias string, amountSat int64, localSat int64, revenue7dSat int64, costPpm int64) RebalanceChannel {
	return RebalanceChannel{
		ChannelID:            id,
		ChannelPoint:         "target-point",
		PeerAlias:            alias,
		Active:               true,
		CapacitySat:          1_000_000,
		LocalBalanceSat:      localSat,
		RemoteBalanceSat:     1_000_000 - localSat,
		LocalPct:             float64(localSat) / 10_000,
		OutgoingFeePpm:       1_000,
		PeerFeeRatePpm:       100,
		TargetOutboundPct:    50,
		TargetAmountSat:      amountSat,
		EligibleAsTarget:     true,
		Revenue7dSat:         revenue7dSat,
		RebalanceCost7dPpm:   costPpm,
		RebalanceAmount7dSat: amountSat,
	}
}

func TestBuildAndOrderRebalanceCandidatesGolden(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	cfg := defaultRebalanceConfig()
	cfg.AutoEnabled = true
	cfg.ROIMin = 0.8
	cfg.ScanIntervalSec = 15 * 60
	cfg.MinSplitEnabled = true
	cfg.MinAmountSat = 50_000
	cfg.MinExecuteSat = 10_000

	channels := []RebalanceChannel{
		rebalanceGoldenSource(1, 500_000),
		rebalanceGoldenSource(2, 300_000),
		rebalanceGoldenTarget(101, "top-recent", 200_000, 400_000, 10_000, 500),     // score 4_900
		rebalanceGoldenTarget(102, "top-older", 200_000, 400_000, 9_600, 500),       // score 4_700, inside top bucket
		rebalanceGoldenTarget(103, "recent-filtered", 200_000, 400_000, 7_600, 500), // score 3_700, filtered by recent auto
		rebalanceGoldenTarget(104, "outside-higher", 200_000, 400_000, 8_800, 500),  // score 4_300, below top bucket
		rebalanceGoldenTarget(105, "cooldown-probe", 200_000, 400_000, 7_000, 500),
		rebalanceGoldenTarget(106, "below-min", 9_000, 400_000, 10_000, 500),
		rebalanceGoldenTarget(107, "roi-guardrail", 100_000, 1_000_000, 500, 10_000),
		rebalanceGoldenTarget(108, "profit-guardrail", 100_000, 100_000, 900, 10_000),
		rebalanceGoldenTarget(109, "cooldown-skip", 200_000, 400_000, 7_000, 500),
		rebalanceGoldenTarget(110, "outside-lower", 200_000, 400_000, 7_400, 500), // score 3_600
	}
	settings := map[uint64]channelSetting{}
	for _, ch := range channels {
		settings[ch.ChannelID] = channelSetting{
			ChannelID:           ch.ChannelID,
			TargetOutboundPct:   ch.TargetOutboundPct,
			AutoEnabled:         true,
			UseDefaultEconRatio: true,
		}
	}
	lastAutoByTarget := map[uint64]time.Time{
		101: now.Add(-1 * time.Hour),
		102: now.Add(-3 * time.Hour),
		103: now.Add(-2 * time.Minute),
		105: now.Add(-20 * time.Minute),
		109: now.Add(-5 * time.Minute),
	}
	recentFailures := recentCooldownStat{
		Attempts:      targetCooldownMinAttempts,
		Failures:      targetCooldownMinAttempts,
		LastAttemptAt: now.Add(-5 * time.Minute),
		LastFailureAt: now.Add(-5 * time.Minute),
	}

	got := buildAndOrderRebalanceCandidates(rebalanceAutoScanCandidateInput{
		Channels:         channels,
		Settings:         settings,
		Cfg:              cfg,
		ScanAt:           now,
		LastAutoByTarget: lastAutoByTarget,
		TargetCooldowns: map[uint64]recentCooldownStat{
			105: recentFailures,
			109: recentFailures,
		},
	})

	if got.EligibleSources != 2 {
		t.Fatalf("eligible sources: got %d, want 2", got.EligibleSources)
	}
	if got.TotalAvailable != 800_000 {
		t.Fatalf("total available: got %d, want 800000", got.TotalAvailable)
	}
	if got.TopScore != 4_900 || !got.TopScoreSet {
		t.Fatalf("top score: got %d set=%v, want 4900 set=true", got.TopScore, got.TopScoreSet)
	}
	if got.BelowExecuteMinSkipped != 1 || got.ROISkipped != 1 || got.ProfitSkipped != 1 || got.TargetCooldownSkipped != 2 || got.RecentSkipped != 1 {
		t.Fatalf("skip counters: below=%d roi=%d profit=%d cooldown=%d recent=%d",
			got.BelowExecuteMinSkipped, got.ROISkipped, got.ProfitSkipped, got.TargetCooldownSkipped, got.RecentSkipped)
	}

	wantReasons := map[string]int{
		"below_execute_min":  1,
		"roi_guardrail":      1,
		"profit_guardrail":   1,
		"target_cooldown":    2,
		"recently_attempted": 1,
	}
	if !reflect.DeepEqual(got.SkipReasons, wantReasons) {
		t.Fatalf("skip reasons:\n got %#v\nwant %#v", got.SkipReasons, wantReasons)
	}

	type wantCandidate struct {
		id            uint64
		score         int64
		cooldownProbe bool
		amountSat     int64
		probeAmount   int64
		original      int64
	}
	wantCandidates := []wantCandidate{
		{id: 102, score: 4_700, amountSat: 200_000},
		{id: 101, score: 4_900, amountSat: 200_000},
		{id: 104, score: 4_300, amountSat: 200_000},
		{id: 110, score: 3_600, amountSat: 200_000},
		{id: 105, score: -1, cooldownProbe: true, amountSat: 50_000, probeAmount: 50_000, original: 200_000},
	}
	if len(got.Candidates) != len(wantCandidates) {
		t.Fatalf("candidates length: got %d, want %d", len(got.Candidates), len(wantCandidates))
	}
	for i, want := range wantCandidates {
		candidate := got.Candidates[i]
		if candidate.Channel.ChannelID != want.id ||
			candidate.Score != want.score ||
			candidate.CooldownProbe != want.cooldownProbe ||
			candidate.Channel.TargetAmountSat != want.amountSat ||
			candidate.ProbeAmountSat != want.probeAmount ||
			candidate.OriginalAmountSat != want.original {
			t.Fatalf("candidate[%d]: got id=%d score=%d probe=%v amount=%d probe_amount=%d original=%d, want %+v",
				i,
				candidate.Channel.ChannelID,
				candidate.Score,
				candidate.CooldownProbe,
				candidate.Channel.TargetAmountSat,
				candidate.ProbeAmountSat,
				candidate.OriginalAmountSat,
				want)
		}
	}

	gotDetails := map[uint64]string{}
	for _, detail := range got.SkippedDetails {
		gotDetails[detail.ChannelID] = detail.Reason
	}
	wantDetails := map[uint64]string{
		106: "below_execute_min",
		107: "roi_guardrail",
		108: "profit_guardrail",
		109: "target_cooldown",
	}
	if !reflect.DeepEqual(gotDetails, wantDetails) {
		t.Fatalf("skipped details:\n got %#v\nwant %#v", gotDetails, wantDetails)
	}
}

func TestBuildAndOrderRebalanceCandidatesGainModelV2UsesVelocity(t *testing.T) {
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	cfg := defaultRebalanceConfig()
	cfg.AutoEnabled = true
	cfg.GainModelVersion = 2
	cfg.VelocityWeight = 0.7
	cfg.ROIMin = 0
	cfg.MinSplitEnabled = true
	cfg.MinExecuteSat = 10_000

	highVelocity := rebalanceGoldenTarget(201, "high-velocity", 1_000_000, 400_000, 1, 100)
	highVelocity.DrainRateSatPerHour = 10_000
	lowVelocity := rebalanceGoldenTarget(202, "low-velocity", 1_000_000, 400_000, 1, 100)
	lowVelocity.DrainRateSatPerHour = 0

	channels := []RebalanceChannel{
		rebalanceGoldenSource(1, 2_000_000),
		lowVelocity,
		highVelocity,
	}
	settings := map[uint64]channelSetting{}
	for _, ch := range channels {
		settings[ch.ChannelID] = channelSetting{
			ChannelID:           ch.ChannelID,
			TargetOutboundPct:   ch.TargetOutboundPct,
			AutoEnabled:         true,
			UseDefaultEconRatio: true,
		}
	}

	got := buildAndOrderRebalanceCandidates(rebalanceAutoScanCandidateInput{
		Channels:         channels,
		Settings:         settings,
		Cfg:              cfg,
		ScanAt:           now,
		LastAutoByTarget: map[uint64]time.Time{},
	})

	if len(got.Candidates) != 2 {
		t.Fatalf("candidates length: got %d, want 2", len(got.Candidates))
	}
	if got.Candidates[0].Channel.ChannelID != 201 {
		t.Fatalf("expected high velocity candidate first, got channel %d", got.Candidates[0].Channel.ChannelID)
	}
	if got.Candidates[0].ExpectedGainSat != 900 {
		t.Fatalf("expected v2 gain 900 sats, got %d", got.Candidates[0].ExpectedGainSat)
	}
	if got.Candidates[0].Score <= got.Candidates[1].Score {
		t.Fatalf("expected velocity-adjusted score to beat low velocity score, got %d <= %d", got.Candidates[0].Score, got.Candidates[1].Score)
	}
}
