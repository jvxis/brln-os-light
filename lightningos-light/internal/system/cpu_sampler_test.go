package system

import (
	"testing"
	"time"
)

func TestParseCPUStatLineCountsIOWaitAsIdle(t *testing.T) {
	idle, total, err := parseCPUStatLine("cpu  100 20 30 400 50 6 7 8 0 0")
	if err != nil {
		t.Fatalf("expected parser to succeed: %v", err)
	}
	if idle != 450 {
		t.Fatalf("expected idle to include iowait, got %d", idle)
	}
	if total != 621 {
		t.Fatalf("expected total 621, got %d", total)
	}
}

func TestAppendCPUUsageSampleTrimsWindow(t *testing.T) {
	base := time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC)
	samples := []cpuUsageSample{
		{At: base.Add(-40 * time.Second), Percent: 10},
		{At: base.Add(-20 * time.Second), Percent: 20},
	}
	updated := appendCPUUsageSample(samples, cpuUsageSample{At: base, Percent: 30}, 30*time.Second)
	if len(updated) != 2 {
		t.Fatalf("expected old sample outside window to be trimmed, got %d samples", len(updated))
	}
	if updated[0].Percent != 20 || updated[1].Percent != 30 {
		t.Fatalf("unexpected trimmed samples: %+v", updated)
	}
}

func TestAverageCPUUsageSamples(t *testing.T) {
	samples := []cpuUsageSample{
		{Percent: 15},
		{Percent: 45},
		{Percent: 30},
	}
	if avg := averageCPUUsageSamples(samples); avg != 30 {
		t.Fatalf("expected average 30, got %v", avg)
	}
}

func TestPreferredCPUPercentUsesAverageWhenAvailable(t *testing.T) {
	snapshot := cpuUsageSnapshot{
		Latest:      90,
		Average30s:  37.5,
		SampleCount: 8,
	}
	if got := preferredCPUPercent(snapshot); got != 37.5 {
		t.Fatalf("expected averaged cpu percent, got %v", got)
	}
}
