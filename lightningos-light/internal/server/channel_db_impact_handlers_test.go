package server

import (
	"math"
	"testing"
	"time"

	"lightningos-light/internal/lndclient"
)

func TestBuildLNChannelDBImpactResponseRanksByUpdateShare(t *testing.T) {
	sizeBytes := int64(10_000_000_000)
	channels := []lndclient.ChannelInfo{
		{
			ChannelPoint:    "b:1",
			ChannelID:       testShortChannelID(850010, 2, 1),
			ChannelIDString: "934595567779840001",
			PeerAlias:       "middle",
			CapacitySat:     10_000_000,
			NumUpdates:      30_000_000,
			Active:          true,
		},
		{
			ChannelPoint:    "a:0",
			ChannelID:       testShortChannelID(850000, 1, 0),
			ChannelIDString: "934584572663808000",
			PeerAlias:       "largest",
			CapacitySat:     5_000_000,
			NumUpdates:      60_000_000,
			Active:          true,
		},
		{
			ChannelPoint:    "c:2",
			ChannelID:       testShortChannelID(850020, 3, 2),
			ChannelIDString: "934606562895872002",
			PeerAlias:       "smallest",
			CapacitySat:     2_000_000,
			NumUpdates:      10_000_000,
			Active:          false,
		},
	}

	resp := buildLNChannelDBImpactResponse("bolt", &sizeBytes, 900000, channels, time.Unix(100, 0))

	if !resp.Available {
		t.Fatal("Available = false, want true")
	}
	if !resp.SizeAvailable {
		t.Fatal("SizeAvailable = false, want true")
	}
	if resp.TotalUpdates != 100_000_000 {
		t.Fatalf("TotalUpdates = %d, want 100000000", resp.TotalUpdates)
	}
	if resp.Top10Updates != resp.TotalUpdates {
		t.Fatalf("Top10Updates = %d, want %d", resp.Top10Updates, resp.TotalUpdates)
	}
	if len(resp.Channels) != 3 {
		t.Fatalf("len(Channels) = %d, want 3", len(resp.Channels))
	}
	if got := resp.Channels[0].PeerAlias; got != "largest" {
		t.Fatalf("top peer = %q, want largest", got)
	}
	assertFloatNear(t, resp.Channels[0].SharePct, 60, 0.0001, "top share")
	if resp.Channels[0].EstimatedDBGB == nil {
		t.Fatal("top EstimatedDBGB = nil")
	}
	assertFloatNear(t, *resp.Channels[0].EstimatedDBGB, 6, 0.0001, "top estimated gb")
	if got := resp.Channels[0].Recommendation; got != "critical" {
		t.Fatalf("top recommendation = %q, want critical", got)
	}
	if got := resp.Channels[2].Recommendation; got != "review" {
		t.Fatalf("smallest recommendation = %q, want review", got)
	}
	if resp.Channels[0].UpdatesPerDay <= 0 {
		t.Fatalf("UpdatesPerDay = %f, want positive", resp.Channels[0].UpdatesPerDay)
	}
}

func TestBuildLNChannelDBImpactResponseUnavailableForPostgres(t *testing.T) {
	resp := buildLNChannelDBImpactResponse("postgres", nil, 0, nil, time.Unix(100, 0))
	if resp.Available {
		t.Fatal("Available = true, want false")
	}
	if resp.DBBackend != "postgres" {
		t.Fatalf("DBBackend = %q, want postgres", resp.DBBackend)
	}
	if resp.TotalUpdates != 0 || len(resp.Channels) != 0 {
		t.Fatalf("unexpected channel data: total=%d len=%d", resp.TotalUpdates, len(resp.Channels))
	}
}

func testShortChannelID(blockHeight int, txIndex int, outputIndex int) uint64 {
	return (uint64(blockHeight) << 40) | (uint64(txIndex) << 16) | uint64(outputIndex)
}

func assertFloatNear(t *testing.T, got float64, want float64, tolerance float64, label string) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Fatalf("%s = %f, want %f", label, got, want)
	}
}
