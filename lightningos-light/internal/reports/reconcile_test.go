package reports

import (
	"testing"
	"time"
)

func TestNormalizedUniqueDatesSortsAndDeduplicates(t *testing.T) {
	loc := time.FixedZone("test", -3*60*60)
	dates := []time.Time{
		time.Date(2026, 8, 15, 22, 0, 0, 0, loc),
		time.Date(2026, 8, 14, 10, 0, 0, 0, loc),
		time.Date(2026, 8, 15, 1, 0, 0, 0, loc),
	}
	got := normalizedUniqueDates(dates, loc)
	if len(got) != 2 || got[0].Format("2006-01-02") != "2026-08-14" || got[1].Format("2006-01-02") != "2026-08-15" {
		t.Fatalf("unexpected normalized dates: %v", got)
	}
}

func TestNormalizedUniqueDatesPreservesSQLDateWestOfUTC(t *testing.T) {
	loc := time.FixedZone("test", -3*60*60)
	got := normalizedUniqueDates([]time.Time{time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}, loc)
	if len(got) != 1 || got[0].Format("2006-01-02") != "2026-08-15" {
		t.Fatalf("SQL date shifted across timezone: %v", got)
	}
}
