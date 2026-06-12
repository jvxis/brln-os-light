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
