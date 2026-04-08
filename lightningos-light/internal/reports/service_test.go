package reports

import (
	"testing"
	"time"
)

func TestCanUsePersistedLiveSnapshotSameDayRecent(t *testing.T) {
	loc := time.FixedZone("BRT", -3*60*60)
	now := time.Date(2026, 4, 2, 12, 0, 0, 0, loc)
	request := BuildTimeRangeForToday(now, loc)
	snapshot := liveSnapshot{
		UpdatedAt: now.Add(-10 * time.Minute),
		Timezone:  loc.String(),
		Range: TimeRange{
			StartLocal: time.Date(2026, 4, 2, 0, 0, 0, 0, loc),
			EndLocal:   now.Add(-10 * time.Minute),
		},
		LookbackHours: 0,
	}

	if !canUsePersistedLiveSnapshot(now, request, snapshot, 0, loc) {
		t.Fatalf("expected recent same-day snapshot fallback to be valid")
	}
}

func TestCanUsePersistedLiveSnapshotRejectsStaleSameDay(t *testing.T) {
	loc := time.FixedZone("BRT", -3*60*60)
	now := time.Date(2026, 4, 2, 12, 0, 0, 0, loc)
	request := BuildTimeRangeForToday(now, loc)
	snapshot := liveSnapshot{
		UpdatedAt: now.Add(-3 * time.Hour),
		Timezone:  loc.String(),
		Range: TimeRange{
			StartLocal: time.Date(2026, 4, 2, 0, 0, 0, 0, loc),
			EndLocal:   time.Date(2026, 4, 2, 9, 0, 0, 0, loc),
		},
		LookbackHours: 0,
	}

	if canUsePersistedLiveSnapshot(now, request, snapshot, 0, loc) {
		t.Fatalf("expected stale same-day snapshot fallback to be rejected")
	}
}

func TestCanUsePersistedLiveSnapshotRejectsPreviousDay(t *testing.T) {
	loc := time.FixedZone("BRT", -3*60*60)
	now := time.Date(2026, 4, 2, 12, 0, 0, 0, loc)
	request := BuildTimeRangeForToday(now, loc)
	snapshot := liveSnapshot{
		UpdatedAt: now.Add(-2 * time.Hour),
		Timezone:  loc.String(),
		Range: TimeRange{
			StartLocal: time.Date(2026, 4, 1, 0, 0, 0, 0, loc),
			EndLocal:   time.Date(2026, 4, 1, 23, 59, 0, 0, loc),
		},
		LookbackHours: 0,
	}

	if canUsePersistedLiveSnapshot(now, request, snapshot, 0, loc) {
		t.Fatalf("expected previous-day snapshot fallback to be rejected")
	}
}

func TestCanUsePersistedLiveSnapshotLookbackAge(t *testing.T) {
	loc := time.FixedZone("UTC", 0)
	now := time.Date(2026, 4, 2, 12, 0, 0, 0, loc)
	request := BuildTimeRangeForLookback(now, loc, 6)
	snapshot := liveSnapshot{
		UpdatedAt: now.Add(-(defaultLivePersistedFallbackAge + time.Minute)),
		Timezone:  loc.String(),
		Range: TimeRange{
			StartLocal: now.Add(-6 * time.Hour),
			EndLocal:   now.Add(-time.Minute),
		},
		LookbackHours: 6,
	}

	if canUsePersistedLiveSnapshot(now, request, snapshot, 6, loc) {
		t.Fatalf("expected stale lookback snapshot fallback to be rejected")
	}
}
