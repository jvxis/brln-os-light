package server

import (
	"context"
	"testing"
	"time"

	"lightningos-light/internal/lndclient"
)

func TestClassifyChannelRankingKeepsStrong30dChannelUnderMonitor(t *testing.T) {
	ch := lndclient.ChannelInfo{
		Active:           true,
		CapacitySat:      10_000_000,
		LocalBalanceSat:  3_000_000,
		RemoteBalanceSat: 6_999_036,
	}
	forward7d := channelTrafficStat{FeeSat: 3017, AmountSat: 1_600_012, Ppm: 1885}
	forward30d := channelTrafficStat{FeeSat: 25_708, AmountSat: 13_708_268, Ppm: 1875}
	rebal7d := channelTrafficStat{FeeSat: 3970, AmountSat: 2_393_034, Ppm: 1659}
	rebal30d := channelTrafficStat{FeeSat: 19_825, AmountSat: 11_930_023, Ppm: 1662}

	state, _, recommendations := classifyChannelRanking(
		ch,
		10_000_000,
		30,
		forward7d,
		forward30d,
		forward7d,
		forward30d,
		channelTrafficStat{},
		channelTrafficStat{},
		rebal7d,
		rebal30d,
		-953,
		5883,
		-953,
		5883,
		15,
		80,
		51,
		368,
		channelHTLCAggregate{Total: 2, Forward: 2},
		48,
		false,
		false,
		false,
	)

	if state != "monitor" {
		t.Fatalf("expected monitor for strong 30d channel, got %s", state)
	}
	for _, recommendation := range recommendations {
		if recommendation.Code == "prepare_coop_close" {
			t.Fatalf("did not expect close recommendation for strong 30d channel")
		}
	}
}

func TestClassifyChannelRankingClosesOnSevereOperationalRisk(t *testing.T) {
	ch := lndclient.ChannelInfo{
		Active:              false,
		InactiveDurationSec: 9 * 24 * 3600,
	}
	state, _, recommendations := classifyChannelRanking(
		ch,
		10_000_000,
		45,
		channelTrafficStat{FeeSat: 200, AmountSat: 50_000},
		channelTrafficStat{FeeSat: 1500, AmountSat: 1_000_000},
		channelTrafficStat{FeeSat: 200, AmountSat: 50_000},
		channelTrafficStat{FeeSat: 1500, AmountSat: 1_000_000},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{FeeSat: 260, AmountSat: 60_000},
		channelTrafficStat{FeeSat: 900, AmountSat: 700_000},
		-60,
		600,
		-60,
		600,
		22,
		62,
		30,
		400,
		channelHTLCAggregate{Total: 10, Liquidity: 5},
		82,
		false,
		false,
		false,
	)

	if state != "close" {
		t.Fatalf("expected close for severe operational risk, got %s", state)
	}
	foundCloseReview := false
	for _, recommendation := range recommendations {
		if recommendation.Code == "review_with_close_manager" {
			foundCloseReview = true
			break
		}
	}
	if !foundCloseReview {
		t.Fatalf("expected close-manager recommendation for severe operational risk")
	}
}

func TestClassifyChannelRankingCreditsAssistedRevenueBeforeClose(t *testing.T) {
	ch := lndclient.ChannelInfo{
		Active:           true,
		CapacitySat:      5_000_000,
		LocalBalanceSat:  500_000,
		RemoteBalanceSat: 4_500_000,
	}
	forward7d := channelTrafficStat{FeeSat: 80, AmountSat: 20_000}
	forward30d := channelTrafficStat{FeeSat: 600, AmountSat: 250_000}
	effectiveForward7d := channelTrafficStat{FeeSat: 530, AmountSat: 620_000}
	effectiveForward30d := channelTrafficStat{FeeSat: 2_350, AmountSat: 3_250_000}
	assisted7d := channelTrafficStat{FeeSat: 900, AmountSat: 1_200_000}
	assisted30d := channelTrafficStat{FeeSat: 3_500, AmountSat: 6_000_000}
	rebal7d := channelTrafficStat{FeeSat: 300, AmountSat: 100_000}
	rebal30d := channelTrafficStat{FeeSat: 1_200, AmountSat: 500_000}

	state, reasons, recommendations := classifyChannelRanking(
		ch,
		5_000_000,
		10,
		forward7d,
		forward30d,
		effectiveForward7d,
		effectiveForward30d,
		assisted7d,
		assisted30d,
		rebal7d,
		rebal30d,
		-220,
		-600,
		230,
		1_150,
		46,
		58,
		70,
		368,
		channelHTLCAggregate{},
		22,
		false,
		false,
		false,
	)

	if state == "close" {
		t.Fatalf("expected assisted revenue to avoid close classification")
	}
	foundAssistReason := false
	for _, reason := range reasons {
		if reason.Code == "assisted_revenue_support" {
			foundAssistReason = true
			break
		}
	}
	if !foundAssistReason {
		t.Fatalf("expected assisted revenue reason to be present")
	}
	foundAssistRecommendation := false
	for _, recommendation := range recommendations {
		if recommendation.Code == "review_inbound_assist_role" {
			foundAssistRecommendation = true
			break
		}
	}
	if !foundAssistRecommendation {
		t.Fatalf("expected assisted revenue recommendation to be present")
	}
}

func TestClassifyChannelRankingMaintainsWithForwardHTLCNoise(t *testing.T) {
	ch := lndclient.ChannelInfo{
		Active:           true,
		CapacitySat:      5_000_000,
		LocalBalanceSat:  2_500_000,
		RemoteBalanceSat: 2_500_000,
	}

	state, _, recommendations := classifyChannelRanking(
		ch,
		5_000_000,
		50,
		channelTrafficStat{FeeSat: 400, AmountSat: 700_000},
		channelTrafficStat{FeeSat: 1_800, AmountSat: 3_000_000},
		channelTrafficStat{FeeSat: 400, AmountSat: 700_000},
		channelTrafficStat{FeeSat: 1_800, AmountSat: 3_000_000},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{FeeSat: 40, AmountSat: 100_000},
		channelTrafficStat{FeeSat: 160, AmountSat: 400_000},
		360,
		1_640,
		360,
		1_640,
		58,
		62,
		70,
		500,
		channelHTLCAggregate{Total: 2_000, Forward: 1_980, Policy: 3, Liquidity: 17},
		24,
		false,
		false,
		false,
	)

	if state != "maintain" {
		t.Fatalf("expected maintain when HTLC pressure is mostly forward-path noise, got %s", state)
	}
	for _, recommendation := range recommendations {
		if recommendation.Code == "review_htlc_failures" {
			t.Fatalf("did not expect HTLC review to block maintain on forward-path noise")
		}
	}
}

func TestClassifyChannelRankingKeepsSevereLocalHTLCRiskUnderMonitor(t *testing.T) {
	ch := lndclient.ChannelInfo{
		Active:           true,
		CapacitySat:      5_000_000,
		LocalBalanceSat:  2_500_000,
		RemoteBalanceSat: 2_500_000,
	}

	state, _, recommendations := classifyChannelRanking(
		ch,
		5_000_000,
		50,
		channelTrafficStat{FeeSat: 400, AmountSat: 700_000},
		channelTrafficStat{FeeSat: 1_800, AmountSat: 3_000_000},
		channelTrafficStat{FeeSat: 400, AmountSat: 700_000},
		channelTrafficStat{FeeSat: 1_800, AmountSat: 3_000_000},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{FeeSat: 40, AmountSat: 100_000},
		channelTrafficStat{FeeSat: 160, AmountSat: 400_000},
		360,
		1_640,
		360,
		1_640,
		58,
		62,
		70,
		500,
		channelHTLCAggregate{Total: 2_000, Forward: 1_700, Policy: 20, Liquidity: 280},
		24,
		false,
		false,
		false,
	)

	if state != "monitor" {
		t.Fatalf("expected monitor for severe local HTLC risk, got %s", state)
	}
	foundReview := false
	for _, recommendation := range recommendations {
		if recommendation.Code == "review_htlc_failures" {
			foundReview = true
			break
		}
	}
	if !foundReview {
		t.Fatalf("expected HTLC review recommendation for severe local HTLC risk")
	}
}

func TestClassifyChannelRankingDefersCloseDuringWarmup(t *testing.T) {
	ch := lndclient.ChannelInfo{
		Active:              false,
		InactiveDurationSec: 9 * 24 * 3600,
	}

	state, _, recommendations := classifyChannelRanking(
		ch,
		10_000_000,
		45,
		channelTrafficStat{FeeSat: 200, AmountSat: 50_000},
		channelTrafficStat{FeeSat: 1_500, AmountSat: 1_000_000},
		channelTrafficStat{FeeSat: 200, AmountSat: 50_000},
		channelTrafficStat{FeeSat: 1_500, AmountSat: 1_000_000},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{FeeSat: 260, AmountSat: 60_000},
		channelTrafficStat{FeeSat: 900, AmountSat: 700_000},
		-60,
		600,
		-60,
		600,
		22,
		62,
		30,
		48,
		channelHTLCAggregate{Total: 10, Liquidity: 5},
		82,
		false,
		false,
		false,
	)

	if state != "monitor" {
		t.Fatalf("expected monitor during warmup, got %s", state)
	}
	for _, recommendation := range recommendations {
		if recommendation.Code == "prepare_coop_close" {
			t.Fatalf("did not expect close recommendation during warmup")
		}
	}
	foundObserve := false
	for _, recommendation := range recommendations {
		if recommendation.Code == "observe_7d_before_close" {
			foundObserve = true
			break
		}
	}
	if !foundObserve {
		t.Fatalf("expected observe_7d_before_close recommendation during warmup")
	}
}

func TestBuildChannelRankingItemIncludesForwardMovement7d(t *testing.T) {
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	ch := lndclient.ChannelInfo{
		ChannelPoint:     "abc:1",
		ChannelID:        123,
		RemotePubkey:     "02abc",
		PeerAlias:        "Peer",
		Active:           true,
		CapacitySat:      1_000_000,
		LocalBalanceSat:  400_000,
		RemoteBalanceSat: 600_000,
	}
	movement := lndclient.ChannelMovement7d{
		ForwardInCount:      7,
		ForwardInAmountSat:  210_000,
		ForwardOutCount:     11,
		ForwardOutAmountSat: 330_000,
	}

	item := buildChannelRankingItem(
		now,
		ch,
		"sink",
		channelTrafficStat{FeeSat: 100, AmountSat: 50_000, Ppm: 2000},
		channelTrafficStat{FeeSat: 400, AmountSat: 200_000, Ppm: 2000},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{FeeSat: 50, AmountSat: 25_000, Ppm: 2000},
		channelTrafficStat{FeeSat: 150, AmountSat: 75_000, Ppm: 2000},
		movement,
		channelPeerAggregate{Score30d: 70, SampleCount: 336},
		channelHTLCAggregate{},
	)

	if item.ForwardInCount7d != movement.ForwardInCount {
		t.Fatalf("expected forward in count %d, got %d", movement.ForwardInCount, item.ForwardInCount7d)
	}
	if item.ForwardInAmountSat7d != movement.ForwardInAmountSat {
		t.Fatalf("expected forward in amount %d, got %d", movement.ForwardInAmountSat, item.ForwardInAmountSat7d)
	}
	if item.ForwardOutCount7d != movement.ForwardOutCount {
		t.Fatalf("expected forward out count %d, got %d", movement.ForwardOutCount, item.ForwardOutCount7d)
	}
	if item.ForwardOutAmountSat7d != movement.ForwardOutAmountSat {
		t.Fatalf("expected forward out amount %d, got %d", movement.ForwardOutAmountSat, item.ForwardOutAmountSat7d)
	}
}

func TestBuildChannelRankingItemTreatsIdlePostRebalanceAsCloseCandidate(t *testing.T) {
	now := time.Date(2026, 5, 2, 19, 55, 0, 0, time.UTC)
	ch := lndclient.ChannelInfo{
		ChannelPoint:     "rebalance-no-payback:0",
		ChannelID:        99,
		RemotePubkey:     "02deadbeef",
		PeerAlias:        "IdlePeer",
		Active:           true,
		CapacitySat:      10_000_000,
		LocalBalanceSat:  13_129,
		RemoteBalanceSat: 9_985_861,
	}

	item := buildChannelRankingItem(
		now,
		ch,
		"",
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{FeeSat: 19, AmountSat: 13_129, Ppm: 1381},
		lndclient.ChannelMovement7d{},
		channelPeerAggregate{Score30d: 51, SampleCount: 719},
		channelHTLCAggregate{},
	)

	if item.TrendDirection != "worsening" {
		t.Fatalf("expected stale rebalance with no payback to be worsening, got %s", item.TrendDirection)
	}
	if item.Score7d >= 24 {
		t.Fatalf("expected no-payback penalty to push score below close threshold, got %d", item.Score7d)
	}
	if item.State != "close" {
		t.Fatalf("expected close candidate before persistence guard, got %s", item.State)
	}
	if !item.CloseCandidate {
		t.Fatalf("expected close candidate flag")
	}
	if !hasChannelRankingReason(item.Reasons, "rebalance_no_payback") {
		t.Fatalf("expected rebalance_no_payback reason")
	}
	if !hasChannelRankingRecommendation(item.Recommendations, "prepare_close_candidate") {
		t.Fatalf("expected prepare_close_candidate recommendation")
	}
}

func TestBuildChannelRankingItemDoesNotFlagRebalanceNoPaybackWithoutFull7dObservation(t *testing.T) {
	now := time.Date(2026, 5, 2, 19, 55, 0, 0, time.UTC)
	ch := lndclient.ChannelInfo{
		ChannelPoint:     "new-node-rebalance:0",
		ChannelID:        103,
		RemotePubkey:     "02newrebal",
		PeerAlias:        "NewRebalancePeer",
		Active:           true,
		CapacitySat:      10_000_000,
		LocalBalanceSat:  13_129,
		RemoteBalanceSat: 9_985_861,
	}

	item := buildChannelRankingItem(
		now,
		ch,
		"",
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{FeeSat: 19, AmountSat: 13_129, Ppm: 1381},
		lndclient.ChannelMovement7d{},
		channelPeerAggregate{Score30d: 51, SampleCount: 48},
		channelHTLCAggregate{},
	)

	if item.State == "close" {
		t.Fatalf("expected insufficient 7d observation to avoid close, got %s", item.State)
	}
	if hasChannelRankingReason(item.Reasons, "rebalance_no_payback") {
		t.Fatalf("did not expect rebalance_no_payback reason without full 7d observation")
	}
	if hasChannelRankingRecommendation(item.Recommendations, "prepare_close_candidate") {
		t.Fatalf("did not expect close candidate recommendation without full 7d observation")
	}
}

func TestBuildChannelRankingItemTreatsNoMovement30dAsCloseCandidate(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	ch := lndclient.ChannelInfo{
		ChannelPoint:     "idle-30d:0",
		ChannelID:        100,
		RemotePubkey:     "02idle",
		PeerAlias:        "Idle30d",
		Active:           true,
		CapacitySat:      5_000_000,
		LocalBalanceSat:  2_500_000,
		RemoteBalanceSat: 2_500_000,
	}

	item := buildChannelRankingItem(
		now,
		ch,
		"",
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{},
		lndclient.ChannelMovement7d{},
		channelPeerAggregate{Score30d: 75, SampleCount: 720},
		channelHTLCAggregate{},
	)

	if item.Score7d >= 24 {
		t.Fatalf("expected 30d idle penalty to push score below close threshold, got %d", item.Score7d)
	}
	if item.State != "close" {
		t.Fatalf("expected no movement in 30d to become close candidate before persistence guard, got %s", item.State)
	}
	if !hasChannelRankingReason(item.Reasons, "no_economic_movement_30d") {
		t.Fatalf("expected no_economic_movement_30d reason")
	}
	if !hasChannelRankingRecommendation(item.Recommendations, "prepare_close_candidate") {
		t.Fatalf("expected prepare_close_candidate recommendation")
	}
}

func TestBuildChannelRankingItemDoesNotCloseIdleChannelWithoutFull30dObservation(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	ch := lndclient.ChannelInfo{
		ChannelPoint:     "new-node-idle:0",
		ChannelID:        102,
		RemotePubkey:     "02newnode",
		PeerAlias:        "NewNodePeer",
		Active:           true,
		CapacitySat:      5_000_000,
		LocalBalanceSat:  2_500_000,
		RemoteBalanceSat: 2_500_000,
	}

	item := buildChannelRankingItem(
		now,
		ch,
		"",
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{},
		lndclient.ChannelMovement7d{},
		channelPeerAggregate{Score30d: 75, SampleCount: 48},
		channelHTLCAggregate{},
	)

	if item.State == "close" {
		t.Fatalf("expected insufficient 30d observation to avoid close, got %s", item.State)
	}
	if item.Score7d < 24 {
		t.Fatalf("expected idle 30d penalty not to apply before full observation, got score %d", item.Score7d)
	}
	if hasChannelRankingReason(item.Reasons, "no_economic_movement_30d") {
		t.Fatalf("did not expect no_economic_movement_30d reason without full observation")
	}
	if hasChannelRankingReason(item.Reasons, "low_usage") {
		t.Fatalf("did not expect low_usage reason without full 7d observation")
	}
}

func TestBuildChannelRankingItemPenalizesIdleChannelAfterFull7dObservation(t *testing.T) {
	now := time.Date(2026, 7, 9, 10, 53, 0, 0, time.UTC)
	ch := lndclient.ChannelInfo{
		ChannelPoint:     "647b200627ed2715a7ffa2f26de82caf05137847c60dfad50fa059333dbf0215:0",
		ChannelID:        6472006272715,
		RemotePubkey:     "02joyeuxnoeuel",
		PeerAlias:        "JoyeuxNoeuel",
		Active:           true,
		CapacitySat:      15_000_000,
		LocalBalanceSat:  150_936,
		RemoteBalanceSat: 14_848_119,
	}

	item := buildChannelRankingItem(
		now,
		ch,
		"",
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{},
		lndclient.ChannelMovement7d{},
		channelPeerAggregate{Score30d: 52, SampleCount: 698},
		channelHTLCAggregate{},
	)

	if item.Score7d != 10 {
		t.Fatalf("expected 7d idle penalty to keep score at 10, got %d", item.Score7d)
	}
	if item.Score30d != 50 {
		t.Fatalf("expected unpenalized 30d comparison score 50 before full 30d observation, got %d", item.Score30d)
	}
	if item.State != "monitor" {
		t.Fatalf("expected idle 7d channel to stay monitor instead of maintain, got %s", item.State)
	}
	if item.CloseCandidate {
		t.Fatalf("did not expect 7d idle channel to become close candidate before full 30d observation")
	}
	if !hasChannelRankingReason(item.Reasons, "low_usage") {
		t.Fatalf("expected low_usage reason after full 7d observation")
	}
	if hasChannelRankingReason(item.Reasons, "no_economic_movement_30d") {
		t.Fatalf("did not expect 30d idle reason before full 30d observation")
	}
}

func TestBuildChannelRankingItemTreatsRebalanceOnly30dAsCloseCandidate(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	ch := lndclient.ChannelInfo{
		ChannelPoint:     "rebalance-only-30d:0",
		ChannelID:        101,
		RemotePubkey:     "02rebalonly",
		PeerAlias:        "RebalanceOnly",
		Active:           true,
		CapacitySat:      5_000_000,
		LocalBalanceSat:  150_000,
		RemoteBalanceSat: 4_850_000,
	}

	item := buildChannelRankingItem(
		now,
		ch,
		"",
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{},
		channelTrafficStat{FeeSat: 60, AmountSat: 100_000, Ppm: 600},
		channelTrafficStat{FeeSat: 60, AmountSat: 100_000, Ppm: 600},
		lndclient.ChannelMovement7d{},
		channelPeerAggregate{Score30d: 75, SampleCount: 720},
		channelHTLCAggregate{},
	)

	if item.Score7d >= 24 {
		t.Fatalf("expected rebalance-only 30d penalty to push score below close threshold, got %d", item.Score7d)
	}
	if item.State != "close" {
		t.Fatalf("expected rebalance-only 30d to become close candidate before persistence guard, got %s", item.State)
	}
	if !hasChannelRankingReason(item.Reasons, "rebalance_only_30d") {
		t.Fatalf("expected rebalance_only_30d reason")
	}
	if !hasChannelRankingRecommendation(item.Recommendations, "stop_nonessential_rebalances") {
		t.Fatalf("expected stop_nonessential_rebalances recommendation")
	}
	if !hasChannelRankingRecommendation(item.Recommendations, "prepare_close_candidate") {
		t.Fatalf("expected prepare_close_candidate recommendation")
	}
}

func TestApplyClosePersistenceGuardAllowsThirtyDayIdleEvidence(t *testing.T) {
	service := &ChannelRankingService{}
	item := ChannelRankingItem{
		ChannelPoint:   "idle-30d:0",
		State:          "close",
		CloseCandidate: true,
		Reasons:        []ChannelRankingReason{{Code: "no_economic_movement_30d"}},
		ComputedAt:     time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC),
	}

	if err := service.applyClosePersistenceGuard(context.Background(), &item); err != nil {
		t.Fatalf("expected 30d idle evidence to bypass persistence lookup, got %v", err)
	}
	if item.State != "close" {
		t.Fatalf("expected state to remain close, got %s", item.State)
	}
}

func TestLatestChannelRankingSyncAt(t *testing.T) {
	base := time.Date(2026, 4, 12, 18, 0, 0, 0, time.UTC)
	later := base.Add(3 * time.Minute)

	tests := []struct {
		name      string
		persisted *time.Time
		inMemory  *time.Time
		want      *time.Time
	}{
		{
			name:      "prefers persisted snapshot after restart",
			persisted: &base,
			inMemory:  nil,
			want:      &base,
		},
		{
			name:      "prefers newer in-memory refresh",
			persisted: &base,
			inMemory:  &later,
			want:      &later,
		},
		{
			name:      "prefers newer persisted snapshot",
			persisted: &later,
			inMemory:  &base,
			want:      &later,
		},
		{
			name:      "returns nil when both are empty",
			persisted: nil,
			inMemory:  nil,
			want:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := latestChannelRankingSyncAt(tt.persisted, tt.inMemory)
			switch {
			case tt.want == nil && got != nil:
				t.Fatalf("expected nil, got %v", *got)
			case tt.want != nil && got == nil:
				t.Fatalf("expected %v, got nil", *tt.want)
			case tt.want != nil && !got.Equal(*tt.want):
				t.Fatalf("expected %v, got %v", *tt.want, *got)
			}
			if got != nil {
				if tt.persisted != nil && got == tt.persisted {
					t.Fatalf("expected cloned pointer instead of persisted input")
				}
				if tt.inMemory != nil && got == tt.inMemory {
					t.Fatalf("expected cloned pointer instead of in-memory input")
				}
			}
		})
	}
}

func hasChannelRankingReason(reasons []ChannelRankingReason, code string) bool {
	for _, reason := range reasons {
		if reason.Code == code {
			return true
		}
	}
	return false
}

func hasChannelRankingRecommendation(recommendations []ChannelRankingRecommendation, code string) bool {
	for _, recommendation := range recommendations {
		if recommendation.Code == code {
			return true
		}
	}
	return false
}
