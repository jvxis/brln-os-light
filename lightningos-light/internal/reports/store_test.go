package reports

import (
  "strings"
  "testing"
  "time"
)

func TestBuildUpsertDaily(t *testing.T) {
  reportDate := time.Date(2026, 1, 15, 0, 0, 0, 0, time.FixedZone("Local", -3*60*60))
  row := Row{
    ReportDate: reportDate,
    Metrics: Metrics{
      ForwardFeeRevenueSat: 1200,
      ForwardFeeRevenueMsat: 1200000,
      RebalanceFeeCostSat: 300,
      RebalanceFeeCostMsat: 300000,
      NetRoutingProfitSat: 900,
      NetRoutingProfitMsat: 900000,
      ForwardCount: 4,
      RebalanceCount: 2,
      RoutedVolumeSat: 18000,
      RoutedVolumeMsat: 18000000,
    },
  }

  query, args := buildUpsertDaily(row)
  if !strings.Contains(query, "on conflict (report_date) do update") {
    t.Fatalf("expected upsert query")
  }
  if !strings.Contains(query, "updated_at = now()") {
    t.Fatalf("expected updated_at update")
  }
  if len(args) != 14 {
    t.Fatalf("expected 14 args, got %d", len(args))
  }

  argDate, ok := args[0].(time.Time)
  if !ok {
    t.Fatalf("expected time arg for report date")
  }
  if argDate.Year() != 2026 || argDate.Month() != 1 || argDate.Day() != 15 {
    t.Fatalf("unexpected report date arg: %v", argDate)
  }
  if args[1] != int64(1200) || args[2] != int64(300) || args[3] != int64(900) {
    t.Fatalf("unexpected metrics args")
  }
}
