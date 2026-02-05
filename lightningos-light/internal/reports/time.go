package reports

import (
  "fmt"
  "strings"
  "time"
)

const (
  RangeD1 = "d-1"
  RangeMonth = "month"
  Range3M = "3m"
  Range6M = "6m"
  Range12M = "12m"
  RangeAll = "all"
)

const maxCustomRangeDays = 730

type DateRange struct {
  StartDate time.Time
  EndDate time.Time
  All bool
}

func ResolveRangeWindow(now time.Time, loc *time.Location, key string) (DateRange, error) {
  if loc == nil {
    loc = time.Local
  }
  today := dateOnly(now, loc)
  yesterday := today.AddDate(0, 0, -1)

  switch key {
  case RangeD1:
    return DateRange{StartDate: yesterday, EndDate: yesterday}, nil
  case RangeMonth:
    start := yesterday.AddDate(0, 0, -29)
    return DateRange{StartDate: start, EndDate: yesterday}, nil
  case Range3M:
    start := yesterday.AddDate(0, 0, -89)
    return DateRange{StartDate: start, EndDate: yesterday}, nil
  case Range6M:
    start := yesterday.AddDate(0, 0, -179)
    return DateRange{StartDate: start, EndDate: yesterday}, nil
  case Range12M:
    start := yesterday.AddDate(0, 0, -364)
    return DateRange{StartDate: start, EndDate: yesterday}, nil
  case RangeAll:
    return DateRange{All: true}, nil
  default:
    return DateRange{}, fmt.Errorf("invalid range: %s", key)
  }
}

func ParseDate(value string, loc *time.Location) (time.Time, error) {
  if loc == nil {
    loc = time.Local
  }
  parsed, err := time.ParseInLocation("2006-01-02", value, loc)
  if err != nil {
    return time.Time{}, err
  }
  return dateOnly(parsed, loc), nil
}

func BuildTimeRangeForDate(date time.Time, loc *time.Location) TimeRange {
  if loc == nil {
    loc = time.Local
  }
  startLocal := dateOnly(date, loc)
  endLocal := startLocal.AddDate(0, 0, 1)
  return TimeRange{
    StartLocal: startLocal,
    EndLocal: endLocal,
    StartUTC: startLocal.UTC(),
    EndUTC: endLocal.UTC(),
  }
}

func BuildTimeRangeForToday(now time.Time, loc *time.Location) TimeRange {
  if loc == nil {
    loc = time.Local
  }
  localNow := now.In(loc)
  startLocal := dateOnly(localNow, loc)
  return TimeRange{
    StartLocal: startLocal,
    EndLocal: localNow,
    StartUTC: startLocal.UTC(),
    EndUTC: localNow.UTC(),
  }
}

func BuildTimeRangeForLookback(now time.Time, loc *time.Location, hours int) TimeRange {
  if loc == nil {
    loc = time.Local
  }
  if hours <= 0 {
    return BuildTimeRangeForToday(now, loc)
  }
  localNow := now.In(loc)
  startLocal := localNow.Add(-time.Duration(hours) * time.Hour)
  return TimeRange{
    StartLocal: startLocal,
    EndLocal: localNow,
    StartUTC: startLocal.UTC(),
    EndUTC: localNow.UTC(),
  }
}

func dateOnly(value time.Time, loc *time.Location) time.Time {
  if loc == nil {
    loc = time.Local
  }
  local := value.In(loc)
  return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}

func ValidateCustomRange(start, end time.Time) error {
  if end.Before(start) {
    return fmt.Errorf("invalid range")
  }
  days := int(end.Sub(start).Hours()/24) + 1
  if days > maxCustomRangeDays {
    return fmt.Errorf("range too large")
  }
  return nil
}

func CustomRangeDaysLimit() int {
  return maxCustomRangeDays
}

func ResolveLocation(name string, fallback *time.Location) (*time.Location, error) {
  if fallback == nil {
    fallback = time.Local
  }
  raw := strings.TrimSpace(name)
  if raw == "" || strings.EqualFold(raw, "local") || strings.EqualFold(raw, "system_local") {
    return fallback, nil
  }
  loc, err := time.LoadLocation(raw)
  if err != nil {
    return fallback, err
  }
  return loc, nil
}
