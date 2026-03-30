package server

import (
	"testing"

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
