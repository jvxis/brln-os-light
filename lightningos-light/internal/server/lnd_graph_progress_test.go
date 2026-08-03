package server

import (
	"strings"
	"testing"
	"time"
)

func TestLNDGraphProgressJournalArgsPutInvocationMatchFirst(t *testing.T) {
	const invocation = "9ae52c4830974e05b35c349e5e9e5814"
	args := lndGraphProgressJournalArgs("lnd", invocation)
	if len(args) == 0 || args[0] != "_SYSTEMD_INVOCATION_ID="+invocation {
		t.Fatalf("invocation selector must be the first journalctl argument: %v", args)
	}
	if !strings.Contains(strings.Join(args, " "), "--grep") {
		t.Fatalf("journalctl arguments lost graph log filter: %v", args)
	}
}

func TestParseLNDGraphProgress(t *testing.T) {
	lines := []string{
		"DISC: GossipSyncer(aaaa): filtering through 35043 chans",
		"DISC: GossipSyncer(aaaa): starting query for 30956 new chans",
		"DISC: GossipSyncer(bbbb): filtering through 38994 chans",
		"DISC: GossipSyncer(bbbb): starting query for 34548 new chans",
		"DISC: GossipSyncer(cccc): filtering through 37945 chans",
		"DISC: GossipSyncer(cccc): starting query for 33455 new chans",
	}

	progress, ok := parseLNDGraphProgress(lines)
	if !ok {
		t.Fatal("expected graph progress")
	}
	if progress.TotalChannels != 38994 {
		t.Fatalf("expected 38994 total channels, got %d", progress.TotalChannels)
	}
	if progress.KnownChannels != 4490 {
		t.Fatalf("expected 4490 known channels, got %d", progress.KnownChannels)
	}
	if progress.RemainingChannels != 34504 {
		t.Fatalf("expected 34504 remaining channels, got %d", progress.RemainingChannels)
	}
	if progress.ProgressPercent != 11.5 {
		t.Fatalf("expected 11.5 percent, got %.1f", progress.ProgressPercent)
	}
	if !progress.Approximate {
		t.Fatal("expected approximate progress")
	}
}

func TestParseLNDGraphProgressWithReverseJournalOrder(t *testing.T) {
	lines := []string{
		"DISC: GossipSyncer(aaaa): starting query for 33353 new chans",
		"DISC: GossipSyncer(aaaa): filtering through 37952 chans",
		"DISC: GossipSyncer(aaaa): starting query for 33437 new chans",
		"DISC: GossipSyncer(aaaa): filtering through 37955 chans",
	}

	progress, ok := parseLNDGraphProgress(lines)
	if !ok {
		t.Fatal("expected graph progress")
	}
	if progress.TotalChannels != 37955 || progress.KnownChannels != 4602 {
		t.Fatalf("unexpected reverse-order progress: %+v", progress)
	}
}

func TestParseLNDGraphProgressIgnoresUnpairedQueries(t *testing.T) {
	lines := []string{
		"DISC: GossipSyncer(aaaa): starting query for 30000 new chans",
		"DISC: GossipSyncer(bbbb): filtering through 38000 chans",
	}

	progress, ok := parseLNDGraphProgress(lines)
	if !ok {
		t.Fatal("expected total-only graph progress")
	}
	if progress.KnownChannels != 0 || progress.ProgressPercent != 0 {
		t.Fatalf("expected zero known progress, got %+v", progress)
	}
}

func TestParseLNDGraphProgressComplete(t *testing.T) {
	lines := []string{
		"DISC: GossipSyncer(aaaa): filtering through 38000 chans",
		"DISC: GossipSyncer(aaaa): no more chans to query",
	}

	progress, ok := parseLNDGraphProgress(lines)
	if !ok {
		t.Fatal("expected completed graph progress")
	}
	if progress.ProgressPercent != 100 || progress.RemainingChannels != 0 {
		t.Fatalf("expected completed progress, got %+v", progress)
	}
}

func TestParseLNDGraphProgressWithoutMarkers(t *testing.T) {
	if progress, ok := parseLNDGraphProgress([]string{"unrelated log"}); ok || progress != nil {
		t.Fatalf("expected no progress, got %+v", progress)
	}
}

func TestApplyGraphProgressRate(t *testing.T) {
	now := time.Now()
	progress := &lndGraphSyncProgress{
		KnownChannels:     4500,
		TotalChannels:     39000,
		RemainingChannels: 34500,
	}
	samples := []lndGraphProgressSample{
		{CheckedAt: now.Add(-10 * time.Minute), KnownChannels: 4000},
		{CheckedAt: now, KnownChannels: 4500},
	}

	applyGraphProgressRate(progress, samples)

	if progress.ChannelsPerHour != 3000 {
		t.Fatalf("expected 3000 channels/hour, got %.0f", progress.ChannelsPerHour)
	}
	if progress.ETASeconds != 41400 {
		t.Fatalf("expected 41400 seconds ETA, got %d", progress.ETASeconds)
	}
}

func TestApplyGraphProgressRateNeedsStableSample(t *testing.T) {
	now := time.Now()
	progress := &lndGraphSyncProgress{RemainingChannels: 1000}
	samples := []lndGraphProgressSample{
		{CheckedAt: now.Add(-time.Minute), KnownChannels: 100},
		{CheckedAt: now, KnownChannels: 200},
	}

	applyGraphProgressRate(progress, samples)

	if progress.ChannelsPerHour != 0 || progress.ETASeconds != 0 {
		t.Fatalf("expected no rate from short sample, got %+v", progress)
	}
}
