package server

import (
	"testing"
	"time"
)

// A seller refilled at 362 ppm (more than the requested amount measured) that
// sells at our 669 ppm fee was dropped at roi_guardrail on every scan: the
// gain cut the peer's 450 ppm last-hop fee while the measured cost already
// paid that same hop, (669-450)/362 = 0.60.
func peerFeeCostSellerTarget() RebalanceChannel {
	return RebalanceChannel{
		ChannelID:            201,
		ChannelPoint:         "seller-point",
		PeerAlias:            "seller",
		Active:               true,
		CapacitySat:          4_000_000,
		LocalBalanceSat:      900_000,
		RemoteBalanceSat:     3_100_000,
		LocalPct:             22.5,
		OutgoingFeePpm:       669,
		PeerFeeRatePpm:       450,
		TargetOutboundPct:    50,
		TargetAmountSat:      1_000_000,
		EligibleAsTarget:     true,
		Revenue7dSat:         6_193,
		RebalanceCost7dPpm:   362,
		RebalanceAmount7dSat: 10_900_000,
	}
}

func peerFeeCostPlan(t *testing.T, peerFeeAsCost bool) rebalanceAutoScanCandidatePlan {
	t.Helper()
	cfg := defaultRebalanceConfig()
	cfg.AutoEnabled = true
	cfg.GainModelVersion = 3
	cfg.ROIMin = 1.0
	cfg.MinAmountSat = 100_000
	cfg.MinExecuteSat = 10_000
	cfg.PeerFeeAsCostEnabled = peerFeeAsCost
	channels := []RebalanceChannel{rebalanceGoldenSource(1, 5_000_000), peerFeeCostSellerTarget()}
	settings := map[uint64]channelSetting{}
	for _, ch := range channels {
		settings[ch.ChannelID] = channelSetting{ChannelID: ch.ChannelID, TargetOutboundPct: ch.TargetOutboundPct, AutoEnabled: true, UseDefaultEconRatio: true}
	}
	return buildAndOrderRebalanceCandidates(rebalanceAutoScanCandidateInput{
		Channels:         channels,
		Settings:         settings,
		Cfg:              cfg,
		ScanAt:           time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
		SovereignRanking: true,
		ReferenceCostPpm: 327,
	})
}

func TestPeerFeeAsCostKeepsMeasuredSellerOutOfROIGuardrail(t *testing.T) {
	legacy := peerFeeCostPlan(t, false)
	if legacy.ROISkipped != 1 || len(legacy.Candidates) != 0 {
		t.Fatalf("legacy model should drop the seller at roi_guardrail: roi_skipped=%d candidates=%d", legacy.ROISkipped, len(legacy.Candidates))
	}
	counted := peerFeeCostPlan(t, true)
	if counted.ROISkipped != 0 || len(counted.Candidates) != 1 {
		t.Fatalf("peer fee counted once should keep the seller: roi_skipped=%d candidates=%d details=%+v", counted.ROISkipped, len(counted.Candidates), counted.SkippedDetails)
	}
	// The cost floor is the peer's hop plus the route floor, above the 362 ppm
	// measured while the peer charged less.
	cfg := defaultRebalanceConfig()
	cfg.GainModelVersion = 3
	cfg.PeerFeeAsCostEnabled = true
	target := peerFeeCostSellerTarget()
	cost := applyPeerFeeCostFloor(estimateHistoricalCost(1_000_000, target.RebalanceCost7dPpm), 1_000_000, target.PeerFeeRatePpm, cfg)
	if want := estimateHistoricalCost(1_000_000, target.PeerFeeRatePpm+cfg.RebalanceCostFloorPpm); cost != want {
		t.Fatalf("cost floor: got %d want %d", cost, want)
	}
	gain := estimateTargetGainForConfig(cfg, target, 1_000_000)
	if roi := float64(gain) / float64(cost); roi < 1.0 {
		t.Fatalf("roi with the peer fee counted once: got %.2f (gain %d cost %d)", roi, gain, cost)
	}
}

func TestApplyPeerFeeCostFloor(t *testing.T) {
	on := defaultRebalanceConfig()
	on.PeerFeeAsCostEnabled = true
	on.RebalanceCostFloorPpm = 50
	off := on
	off.PeerFeeAsCostEnabled = false
	for _, tc := range []struct {
		name    string
		cfg     RebalanceConfig
		cost    int64
		peerPpm int64
		want    int64
	}{
		{name: "off keeps the estimate", cfg: off, cost: 362, peerPpm: 450, want: 362},
		{name: "measured below the peer hop", cfg: on, cost: 362, peerPpm: 450, want: 500},
		{name: "measured above the peer hop", cfg: on, cost: 643, peerPpm: 500, want: 643},
		{name: "peer charges nothing", cfg: on, cost: 120, peerPpm: 0, want: 120},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := applyPeerFeeCostFloor(tc.cost, 1_000_000, tc.peerPpm, tc.cfg); got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
}

func TestPeerFeeAsCostGainKeepsFullOutgoingFee(t *testing.T) {
	cfg := defaultRebalanceConfig()
	cfg.GainModelVersion = 2
	ch := RebalanceChannel{OutgoingFeePpm: 1_000, PeerFeeRatePpm: 400}
	if got := estimateTargetGainForConfig(cfg, ch, 1_000_000); got != 600 {
		t.Fatalf("legacy v2 gain: got %d want 600", got)
	}
	cfg.PeerFeeAsCostEnabled = true
	if got := estimateTargetGainForConfig(cfg, ch, 1_000_000); got != 1_000 {
		t.Fatalf("v2 gain with the peer fee as cost: got %d want 1000", got)
	}
}

func TestAutopilotReasonShowsFunnelSkip(t *testing.T) {
	cfg := defaultRebalanceConfig()
	cfg.SchedulerMode = rebalanceSchedulerModeSovereignLive
	ch := peerFeeCostSellerTarget()
	ch.AutoEnabled = true
	ch.EligibleAsManualTarget = true
	svc := &RebalanceService{}
	svc.lastSovereignDecisionAt = time.Date(2026, 11, 1, 9, 46, 0, 0, time.UTC)
	svc.recordSovereignFunnelSkips([]RebalanceSkipDetail{{ChannelID: ch.ChannelID, Reason: "roi_guardrail"}})
	channels := []RebalanceChannel{ch}
	svc.annotateAutopilotReasons(cfg, channels)
	if channels[0].AutopilotReason != "roi_guardrail" {
		t.Fatalf("reason: got %q want roi_guardrail", channels[0].AutopilotReason)
	}

	// A sovereign decision for the same channel wins over the funnel skip.
	svc.lastSovereignDecisions = []RebalanceSovereignDecision{{ChannelID: ch.ChannelID, Reason: "paid_stock_gate"}}
	svc.annotateAutopilotReasons(cfg, channels)
	if channels[0].AutopilotReason != "paid_stock_gate" {
		t.Fatalf("reason: got %q want paid_stock_gate", channels[0].AutopilotReason)
	}
}
