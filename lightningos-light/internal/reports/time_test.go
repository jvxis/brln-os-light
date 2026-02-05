package reports

import (
  "testing"
  "time"
)

func TestResolveRangeWindow(t *testing.T) {
  loc := time.FixedZone("UTC", 0)
  now := time.Date(2026, 1, 16, 10, 0, 0, 0, loc)

  dr, err := ResolveRangeWindow(now, loc, RangeD1)
  if err != nil {
    t.Fatalf("expected no error: %v", err)
  }
  want := time.Date(2026, 1, 15, 0, 0, 0, 0, loc)
  if !sameDate(dr.StartDate, want) || !sameDate(dr.EndDate, want) {
    t.Fatalf("unexpected d-1 range: %v -> %v", dr.StartDate, dr.EndDate)
  }

  dr, err = ResolveRangeWindow(now, loc, RangeMonth)
  if err != nil {
    t.Fatalf("expected no error: %v", err)
  }
  wantStart := want.AddDate(0, 0, -29)
  if !sameDate(dr.StartDate, wantStart) || !sameDate(dr.EndDate, want) {
    t.Fatalf("unexpected month range: %v -> %v", dr.StartDate, dr.EndDate)
  }

  dr, err = ResolveRangeWindow(now, loc, Range3M)
  if err != nil {
    t.Fatalf("expected no error: %v", err)
  }
  wantStart = want.AddDate(0, 0, -89)
  if !sameDate(dr.StartDate, wantStart) || !sameDate(dr.EndDate, want) {
    t.Fatalf("unexpected 3m range: %v -> %v", dr.StartDate, dr.EndDate)
  }

  dr, err = ResolveRangeWindow(now, loc, Range6M)
  if err != nil {
    t.Fatalf("expected no error: %v", err)
  }
  wantStart = want.AddDate(0, 0, -179)
  if !sameDate(dr.StartDate, wantStart) || !sameDate(dr.EndDate, want) {
    t.Fatalf("unexpected 6m range: %v -> %v", dr.StartDate, dr.EndDate)
  }

  dr, err = ResolveRangeWindow(now, loc, Range12M)
  if err != nil {
    t.Fatalf("expected no error: %v", err)
  }
  wantStart = want.AddDate(0, 0, -364)
  if !sameDate(dr.StartDate, wantStart) || !sameDate(dr.EndDate, want) {
    t.Fatalf("unexpected 12m range: %v -> %v", dr.StartDate, dr.EndDate)
  }
}

func TestTimeRangeInclusiveEnd(t *testing.T) {
  loc := time.FixedZone("UTC", 0)
  date := time.Date(2026, 1, 15, 12, 0, 0, 0, loc)
  tr := BuildTimeRangeForDate(date, loc)
  expected := tr.EndUTC.Add(-time.Second).Unix()
  if got := int64(tr.EndUnixInclusive()); got != expected {
    t.Fatalf("expected end inclusive %d, got %d", expected, got)
  }
}

func TestResolveLocation(t *testing.T) {
  fallback := time.FixedZone("Fallback", -3*60*60)

  loc, err := ResolveLocation("", fallback)
  if err != nil {
    t.Fatalf("expected no error for empty location: %v", err)
  }
  if loc.String() != fallback.String() {
    t.Fatalf("expected fallback location, got %s", loc.String())
  }

  loc, err = ResolveLocation("UTC", fallback)
  if err != nil {
    t.Fatalf("expected valid timezone: %v", err)
  }
  if loc.String() != "UTC" {
    t.Fatalf("expected UTC, got %s", loc.String())
  }

  loc, err = ResolveLocation("invalid/zone", fallback)
  if err == nil {
    t.Fatalf("expected error for invalid timezone")
  }
  if loc.String() != fallback.String() {
    t.Fatalf("expected fallback on invalid timezone, got %s", loc.String())
  }
}

func sameDate(a, b time.Time) bool {
  return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}
