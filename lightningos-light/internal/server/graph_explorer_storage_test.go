package server

import (
	"testing"
	"time"
)

func TestGraphExplorerEffectiveHistoryDays(t *testing.T) {
	bytesPerDay := float64(1_000_000_000)

	tests := []struct {
		name          string
		retentionDays int
		maxBytes      int64
		want          int
	}{
		{name: "days only", retentionDays: 90, maxBytes: 0, want: 90},
		{name: "size cap below days", retentionDays: 90, maxBytes: 30_000_000_000, want: 30},
		{name: "size cap above days", retentionDays: 30, maxBytes: 90_000_000_000, want: 30},
		{name: "minimum one day", retentionDays: 90, maxBytes: 100_000_000, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := graphExplorerEffectiveHistoryDays(tt.retentionDays, tt.maxBytes, bytesPerDay); got != tt.want {
				t.Fatalf("effective days = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGraphExplorerStorageBytesFromGB(t *testing.T) {
	got, err := graphExplorerStorageBytesFromGB(5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := int64(5 * 1024 * 1024 * 1024); got != want {
		t.Fatalf("bytes = %d, want %d", got, want)
	}

	got, err = graphExplorerStorageBytesFromGB(0)
	if err != nil {
		t.Fatalf("zero should disable size cap: %v", err)
	}
	if got != 0 {
		t.Fatalf("zero cap = %d, want 0", got)
	}

	if _, err := graphExplorerStorageBytesFromGB(-1); err == nil {
		t.Fatal("expected negative size to be rejected")
	}
}

func TestGraphExplorerCleanupAvailableUsesLiveSizing(t *testing.T) {
	const gb = int64(1024 * 1024 * 1024)

	if graphExplorerCleanupAvailable(7.1, 7, 1*gb, 2*gb) {
		t.Fatal("physical bloat alone must not keep logical cleanup available")
	}
	if !graphExplorerCleanupAvailable(46, 7, 1*gb, 2*gb) {
		t.Fatal("coverage older than the effective window should require cleanup")
	}
	if !graphExplorerCleanupAvailable(7.1, 7, 3*gb, 2*gb) {
		t.Fatal("live estimated data above the size cap should require cleanup")
	}
}

func TestGraphExplorerHistorySinceUsesEffectiveCoverage(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	rangeSince := now.AddDate(0, 0, -90)
	coverageSince := now.AddDate(0, 0, -30)

	got := graphExplorerHistorySince(&rangeSince, &coverageSince)
	if got == nil || !got.Equal(coverageSince) {
		t.Fatalf("expected coverage since %v, got %v", coverageSince, got)
	}

	recentRange := now.AddDate(0, 0, -7)
	got = graphExplorerHistorySince(&recentRange, &coverageSince)
	if got == nil || !got.Equal(recentRange) {
		t.Fatalf("expected requested recent range %v, got %v", recentRange, got)
	}
}

func TestGraphExplorerClampFeeCoverageUsesConfiguredRetention(t *testing.T) {
	now := time.Now().UTC()
	oldCoverage := now.AddDate(0, 0, -120)

	got := graphExplorerClampFeeCoverage(&oldCoverage, 30)
	if got == nil {
		t.Fatal("expected coverage floor")
	}
	ageDays := now.Sub(*got).Hours() / 24
	if ageDays < 29 || ageDays > 31 {
		t.Fatalf("expected roughly 30d floor, got %.2fd", ageDays)
	}
}

func TestGraphExplorerStorageConfigOwnRetention(t *testing.T) {
	cfg := normalizeGraphExplorerStorageConfig(GraphExplorerStorageConfig{HistoryRetentionDays: 30, HistoryMaxBytes: graphExplorerDefaultHistoryMaxBytes})
	if cfg.OwnHistoryRetentionDays != graphExplorerDefaultOwnHistoryRetentionDays {
		t.Fatalf("unset own retention takes the default, got %d", cfg.OwnHistoryRetentionDays)
	}
	cfg = normalizeGraphExplorerStorageConfig(GraphExplorerStorageConfig{HistoryRetentionDays: 90, HistoryMaxBytes: graphExplorerDefaultHistoryMaxBytes, OwnHistoryRetentionDays: 30})
	if cfg.OwnHistoryRetentionDays != 90 {
		t.Fatalf("own retention never drops below the graph window, got %d", cfg.OwnHistoryRetentionDays)
	}
	cfg = normalizeGraphExplorerStorageConfig(GraphExplorerStorageConfig{HistoryRetentionDays: 30, HistoryMaxBytes: graphExplorerDefaultHistoryMaxBytes, OwnHistoryRetentionDays: 5000})
	if cfg.OwnHistoryRetentionDays != graphExplorerMaxOwnHistoryRetentionDays {
		t.Fatalf("own retention is capped at %d, got %d", graphExplorerMaxOwnHistoryRetentionDays, cfg.OwnHistoryRetentionDays)
	}
	if err := graphExplorerValidateStorageConfig(GraphExplorerStorageConfig{HistoryRetentionDays: 60, HistoryMaxBytes: 0, OwnHistoryRetentionDays: 45}); err == nil {
		t.Fatal("own retention below the graph window must be rejected")
	}
	if err := graphExplorerValidateStorageConfig(GraphExplorerStorageConfig{HistoryRetentionDays: 60, HistoryMaxBytes: 0, OwnHistoryRetentionDays: 365}); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestGraphExplorerPolicyHistoryCutoffs(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	cutoff, ownCutoff := graphExplorerPolicyHistoryCutoffs(now, 30, 365)
	if cutoff != now.AddDate(0, 0, -30) {
		t.Fatalf("graph cutoff = %s", cutoff)
	}
	if ownCutoff != now.AddDate(0, 0, -365) {
		t.Fatalf("own cutoff = %s", ownCutoff)
	}
	// The own window never ends after the graph window.
	cutoff, ownCutoff = graphExplorerPolicyHistoryCutoffs(now, 90, 30)
	if !ownCutoff.Equal(cutoff) {
		t.Fatalf("own cutoff %s must not be after graph cutoff %s", ownCutoff, cutoff)
	}
	// And it is capped at the maximum.
	_, ownCutoff = graphExplorerPolicyHistoryCutoffs(now, 30, 10_000)
	if ownCutoff != now.AddDate(0, 0, -graphExplorerMaxOwnHistoryRetentionDays) {
		t.Fatalf("own cutoff beyond the cap = %s", ownCutoff)
	}
}
