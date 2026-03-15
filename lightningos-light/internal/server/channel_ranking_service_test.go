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
		rebal7d,
		rebal30d,
		-953,
		5883,
		15,
		80,
		51,
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
		channelTrafficStat{FeeSat: 260, AmountSat: 60_000},
		channelTrafficStat{FeeSat: 900, AmountSat: 700_000},
		-60,
		600,
		22,
		62,
		30,
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
