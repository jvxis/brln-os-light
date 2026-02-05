package main

import (
  "context"
  "flag"
  "log"
  "os"
  "strings"
  "time"

  "lightningos-light/internal/config"
  "lightningos-light/internal/lndclient"
  "lightningos-light/internal/reports"
  "lightningos-light/internal/server"

  "github.com/jackc/pgx/v5/pgxpool"
)

func main() {
  if len(os.Args) > 1 {
    switch os.Args[1] {
    case "reports-run":
      runReports(os.Args[2:])
      return
    case "reports-backfill":
      runReportsBackfill(os.Args[2:])
      return
    }
  }

  runServer(os.Args[1:])
}

func runServer(args []string) {
  fs := flag.NewFlagSet("lightningos-manager", flag.ExitOnError)
  configPath := fs.String("config", "/etc/lightningos/config.yaml", "Path to config.yaml")
  _ = fs.Parse(args)

  cfg, err := config.Load(*configPath)
  if err != nil {
    log.Fatalf("config load failed: %v", err)
  }

  logger := log.New(os.Stdout, "", log.LstdFlags)
  srv := server.New(cfg, logger)

  if err := srv.Run(); err != nil {
    logger.Fatalf("server exited: %v", err)
  }
}

func runReports(args []string) {
  fs := flag.NewFlagSet("reports-run", flag.ExitOnError)
  configPath := fs.String("config", "/etc/lightningos/config.yaml", "Path to config.yaml")
  dateStr := fs.String("date", "", "Report date (YYYY-MM-DD), defaults to yesterday")
  _ = fs.Parse(args)

  cfg, err := config.Load(*configPath)
  if err != nil {
    log.Fatalf("config load failed: %v", err)
  }

  logger := log.New(os.Stdout, "", log.LstdFlags)
  dsn, err := server.ResolveNotificationsDSN(logger)
  if err != nil {
    logger.Fatalf("reports-run failed: %v", err)
  }

  ctx, cancel := context.WithTimeout(context.Background(), reportsRunTimeout())
  defer cancel()

  pool, err := pgxpool.New(ctx, dsn)
  if err != nil {
    logger.Fatalf("reports-run failed: %v", err)
  }
  defer pool.Close()

  lnd := lndclient.New(cfg, logger)
  svc := reports.NewService(pool, lnd, logger)
  if err := svc.EnsureSchema(ctx); err != nil {
    logger.Fatalf("reports-run failed: %v", err)
  }

  loc := resolveReportsLocation(logger)
  reportDate := time.Now().In(loc).AddDate(0, 0, -1)
  if strings.TrimSpace(*dateStr) != "" {
    parsed, err := reports.ParseDate(*dateStr, loc)
    if err != nil {
      logger.Fatalf("reports-run failed: invalid date")
    }
    reportDate = parsed
  }

  row, err := svc.RunDaily(ctx, reportDate, loc, nil)
  if err != nil {
    logger.Fatalf("reports-run failed: %v", err)
  }

  logger.Printf(
    "reports: stored %s (revenue %d sats, cost %d sats, net %d sats)",
    row.ReportDate.Format("2006-01-02"),
    row.Metrics.ForwardFeeRevenueSat,
    row.Metrics.RebalanceFeeCostSat,
    row.Metrics.NetRoutingProfitSat,
  )
}

func runReportsBackfill(args []string) {
  fs := flag.NewFlagSet("reports-backfill", flag.ExitOnError)
  configPath := fs.String("config", "/etc/lightningos/config.yaml", "Path to config.yaml")
  fromStr := fs.String("from", "", "Start date (YYYY-MM-DD)")
  toStr := fs.String("to", "", "End date (YYYY-MM-DD)")
  maxDays := fs.Int("max-days", 0, "Override max range in days (0 uses default limit)")
  _ = fs.Parse(args)

  if strings.TrimSpace(*fromStr) == "" || strings.TrimSpace(*toStr) == "" {
    log.Fatalf("reports-backfill failed: --from and --to are required")
  }

  cfg, err := config.Load(*configPath)
  if err != nil {
    log.Fatalf("config load failed: %v", err)
  }

  logger := log.New(os.Stdout, "", log.LstdFlags)
  dsn, err := server.ResolveNotificationsDSN(logger)
  if err != nil {
    logger.Fatalf("reports-backfill failed: %v", err)
  }

  pool, err := pgxpool.New(context.Background(), dsn)
  if err != nil {
    logger.Fatalf("reports-backfill failed: %v", err)
  }
  defer pool.Close()

  lnd := lndclient.New(cfg, logger)
  svc := reports.NewService(pool, lnd, logger)
  schemaCtx, schemaCancel := context.WithTimeout(context.Background(), 30*time.Second)
  if err := svc.EnsureSchema(schemaCtx); err != nil {
    schemaCancel()
    logger.Fatalf("reports-backfill failed: %v", err)
  }
  schemaCancel()

  loc := resolveReportsLocation(logger)
  startDate, err := reports.ParseDate(*fromStr, loc)
  if err != nil {
    logger.Fatalf("reports-backfill failed: invalid --from date")
  }
  endDate, err := reports.ParseDate(*toStr, loc)
  if err != nil {
    logger.Fatalf("reports-backfill failed: invalid --to date")
  }
  if endDate.Before(startDate) {
    logger.Fatalf("reports-backfill failed: invalid range")
  }
  limit := *maxDays
  if limit <= 0 {
    limit = reports.CustomRangeDaysLimit()
  }
  days := int(endDate.Sub(startDate).Hours()/24) + 1
  if days > limit {
    logger.Fatalf("reports-backfill failed: range too large (max %d days)", limit)
  }

  logger.Printf("reports: backfill %s -> %s", startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))

  startLocal := time.Date(startDate.Year(), startDate.Month(), startDate.Day(), 0, 0, 0, 0, loc)
  endLocal := time.Date(endDate.Year(), endDate.Month(), endDate.Day(), 23, 59, 59, 0, loc)
  rebalanceByDay, err := reports.FetchRebalanceFeesByDay(context.Background(), lnd, uint64(startLocal.UTC().Unix()), uint64(endLocal.UTC().Unix()), loc)
  if err != nil {
    logger.Fatalf("reports-backfill failed: %v", err)
  }
  for day := startDate; !day.After(endDate); day = day.AddDate(0, 0, 1) {
    dayCtx, dayCancel := context.WithTimeout(context.Background(), reportsRunTimeout())
    dayKey := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
    override := rebalanceByDay[dayKey]
    row, err := svc.RunDaily(dayCtx, day, loc, &override)
    dayCancel()
    if err != nil {
      logger.Fatalf("reports-backfill failed on %s: %v", day.Format("2006-01-02"), err)
    }
    logger.Printf(
      "reports: stored %s (revenue %d sats, cost %d sats, net %d sats)",
      row.ReportDate.Format("2006-01-02"),
      row.Metrics.ForwardFeeRevenueSat,
      row.Metrics.RebalanceFeeCostSat,
      row.Metrics.NetRoutingProfitSat,
    )
  }
}

func reportsRunTimeout() time.Duration {
  raw := strings.TrimSpace(os.Getenv("REPORTS_RUN_TIMEOUT_SEC"))
  if raw == "" {
    return 2 * time.Minute
  }
  if parsed, err := time.ParseDuration(raw + "s"); err == nil && parsed > 0 {
    return parsed
  }
  return 2 * time.Minute
}

func resolveReportsLocation(logger *log.Logger) *time.Location {
  raw := strings.TrimSpace(os.Getenv("REPORTS_TIMEZONE"))
  loc, err := reports.ResolveLocation(raw, time.Local)
  if err != nil {
    if logger != nil {
      logger.Printf("reports: invalid REPORTS_TIMEZONE %q, using %s: %v", raw, loc.String(), err)
    }
    return loc
  }
  return loc
}
