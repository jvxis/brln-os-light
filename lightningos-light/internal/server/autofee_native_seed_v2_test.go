package server

import (
	"testing"
	"time"
)

const (
	seedV2Peer  = "02peer"
	seedV2Self  = "03self"
	seedV2Other = "02other"
)

func seedV2Day(base time.Time, day int, hour int) time.Time {
	return base.AddDate(0, 0, day).Add(time.Duration(hour) * time.Hour)
}

func TestNativeSeedV2ExcludesOwnPolicyAndDedupesPerDay(t *testing.T) {
	since := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	end := since.AddDate(0, 0, 3)
	rows := []nativeSeedV2Row{
		// Our own policy toward the peer: must never count as market inbound.
		{ChanID: 1, Advertising: seedV2Self, Connecting: seedV2Peer, Ppm: 5000, CapacitySat: 10_000_000, CapturedAt: seedV2Day(since, 0, 1)},
		{ChanID: 1, Advertising: seedV2Self, Connecting: seedV2Peer, Ppm: 5000, CapacitySat: 10_000_000, CapturedAt: seedV2Day(since, 1, 1)},
		{ChanID: 1, Advertising: seedV2Self, Connecting: seedV2Peer, Ppm: 5000, CapacitySat: 10_000_000, CapturedAt: seedV2Day(since, 2, 1)},
		// Another node toward the peer, re-announced 3x on day 0 (same value).
		{ChanID: 2, Advertising: seedV2Other, Connecting: seedV2Peer, Ppm: 100, CapacitySat: 5_000_000, CapturedAt: seedV2Day(since, 0, 2)},
		{ChanID: 2, Advertising: seedV2Other, Connecting: seedV2Peer, Ppm: 100, CapacitySat: 5_000_000, CapturedAt: seedV2Day(since, 0, 5)},
		{ChanID: 2, Advertising: seedV2Other, Connecting: seedV2Peer, Ppm: 100, CapacitySat: 5_000_000, CapturedAt: seedV2Day(since, 0, 9)},
		// A third node, announced once on day 0 and never again (carry forward).
		{ChanID: 3, Advertising: "02third", Connecting: seedV2Peer, Ppm: 300, CapacitySat: 5_000_000, CapturedAt: seedV2Day(since, 0, 3)},
	}
	inbound, _, stats := buildNativeSeedV2Series(rows, seedV2Peer, seedV2Self, since, end)
	if len(inbound) != 3 {
		t.Fatalf("expected 3 daily samples (carry forward), got %d: %v", len(inbound), inbound)
	}
	for i, v := range inbound {
		if v >= 1000 {
			t.Fatalf("day %d: own 5000 ppm policy leaked into the inbound sample: %v", i, v)
		}
	}
	if stats.SelfExcluded != 3 {
		t.Fatalf("expected own policy excluded on each of 3 days, got %d", stats.SelfExcluded)
	}
	if stats.Samples != 6 {
		t.Fatalf("expected 2 channels x 3 days = 6 samples after dedup/carry, got %d", stats.Samples)
	}
	if stats.Channels != 2 {
		t.Fatalf("expected 2 distinct market channels, got %d", stats.Channels)
	}
	if stats.Deduped != 2 {
		t.Fatalf("expected 2 duplicate announcements folded, got %d", stats.Deduped)
	}
}

func TestNativeSeedV2ExcludesDisabledAndClosed(t *testing.T) {
	since := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	end := since.AddDate(0, 0, 2)
	closedDay1 := seedV2Day(since, 1, 0)
	rows := []nativeSeedV2Row{
		{ChanID: 2, Advertising: seedV2Other, Connecting: seedV2Peer, Ppm: 100, CapacitySat: 5_000_000, CapturedAt: seedV2Day(since, 0, 2)},
		{ChanID: 4, Advertising: "02dis", Connecting: seedV2Peer, Ppm: 9000, CapacitySat: 5_000_000, Disabled: true, CapturedAt: seedV2Day(since, 0, 2)},
		{ChanID: 5, Advertising: "02closed", Connecting: seedV2Peer, Ppm: 9000, CapacitySat: 5_000_000, CapturedAt: seedV2Day(since, 0, 2), ClosedAt: &closedDay1},
	}
	inbound, _, stats := buildNativeSeedV2Series(rows, seedV2Peer, seedV2Self, since, end)
	if len(inbound) != 2 {
		t.Fatalf("expected 2 daily samples, got %d", len(inbound))
	}
	// Day 0: channel 5 still open, so 100 and 9000 both count; day 1: only 100.
	if inbound[1] != 100 {
		t.Fatalf("closed channel must drop out on the day after close, got %v", inbound[1])
	}
	if stats.DisabledExcluded != 2 {
		t.Fatalf("expected disabled direction excluded on both days, got %d", stats.DisabledExcluded)
	}
	if stats.ClosedExcluded != 1 {
		t.Fatalf("expected closed channel excluded once (day 1), got %d", stats.ClosedExcluded)
	}
}

func TestNativeSeedV2InsufficientWithSingleChannel(t *testing.T) {
	since := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	end := since.AddDate(0, 0, 7)
	rows := []nativeSeedV2Row{
		{ChanID: 2, Advertising: seedV2Other, Connecting: seedV2Peer, Ppm: 100, CapacitySat: 5_000_000, CapturedAt: seedV2Day(since, 0, 2)},
	}
	result := buildNativeSeedV2Result(rows, seedV2Peer, seedV2Self, since, end, 0)
	if result.Ok {
		t.Fatal("one market channel is not a market: expected insufficient")
	}
	if len(result.Tags) != 1 || result.Tags[0] != "seed:native-v2-insufficient" {
		t.Fatalf("expected insufficient tag, got %v", result.Tags)
	}
}

func TestNativeSeedV2ResultMatchesLegacyMathOnCleanSeries(t *testing.T) {
	since := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	end := since.AddDate(0, 0, 4)
	rows := []nativeSeedV2Row{}
	for day := 0; day < 4; day++ {
		rows = append(rows,
			nativeSeedV2Row{ChanID: 2, Advertising: seedV2Other, Connecting: seedV2Peer, Ppm: 100, CapacitySat: 5_000_000, CapturedAt: seedV2Day(since, day, 1)},
			nativeSeedV2Row{ChanID: 3, Advertising: "02third", Connecting: seedV2Peer, Ppm: 300, CapacitySat: 5_000_000, CapturedAt: seedV2Day(since, day, 1)},
			nativeSeedV2Row{ChanID: 9, Advertising: seedV2Peer, Connecting: seedV2Other, Ppm: 400, CapacitySat: 5_000_000, CapturedAt: seedV2Day(since, day, 1)},
		)
	}
	result := buildNativeSeedV2Result(rows, seedV2Peer, seedV2Self, since, end, 0)
	if !result.Ok {
		t.Fatalf("expected sufficient series, tags=%v stats=%+v", result.Tags, result.Stats)
	}
	// Legacy math on the same daily series must give the same seed.
	inbound, outbound, _ := buildNativeSeedV2Series(rows, seedV2Peer, seedV2Self, since, end)
	seed, p95, skew, _, ok := computeNativeSeedV2FromSeries(inbound, outbound, 0)
	if !ok || seed != result.Seed || p95 != result.SeedP95 || skew != result.PeerMarketSkew {
		t.Fatalf("result must equal the series computation: %v vs %v", result.Seed, seed)
	}
	if result.Seed <= 0 || result.Seed > 400 {
		t.Fatalf("seed out of the plausible market range: %v", result.Seed)
	}
	if got := nativeSeedV2DeltaPct(200, 250); got != 25 {
		t.Fatalf("expected +25%% delta, got %v", got)
	}
	if got := nativeSeedV2DeltaPct(0, 250); got != 0 {
		t.Fatalf("delta without an applied seed must be 0, got %v", got)
	}
}
