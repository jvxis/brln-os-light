package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"lightningos-light/internal/config"
	"lightningos-light/internal/lndclient"
	"lightningos-light/internal/privileged"
	"lightningos-light/internal/reports"
	"lightningos-light/internal/server"
	"lightningos-light/internal/system"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "auth":
			runAuth(os.Args[2:])
			return
		case "reports-run":
			runReports(os.Args[2:])
			return
		case "reports-backfill":
			runReportsBackfill(os.Args[2:])
			return
		case "reports-reconcile":
			runReportsReconcile(os.Args[2:])
			return
		case "broker-self-test":
			runBrokerSelfTest()
			return
		case "lnd-manager-credential-ensure":
			runLNDManagerCredential(false)
			return
		case "lnd-manager-credential-rollback":
			runLNDManagerCredential(true)
			return
		}
	}

	runServer(os.Args[1:])
}

func runLNDManagerCredential(rollback bool) {
	client, err := privileged.NewClient(string(privileged.ModeEnforce), 20*time.Second, nil)
	if err != nil {
		log.Fatalf("privileged broker client failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var state privileged.LNDManagerCredentialState
	if rollback {
		state, err = client.RollbackLNDManagerCredential(ctx, false)
	} else {
		state, err = client.EnsureLNDManagerCredential(ctx, false)
	}
	if err != nil {
		log.Fatalf("LND manager credential operation failed: %v", err)
	}
	fmt.Printf("status=%s changed=%t\n", state.Status, state.Changed)
}

func runBrokerSelfTest() {
	client, err := privileged.NewClient(string(privileged.ModeEnforce), 10*time.Second, nil)
	if err != nil {
		log.Fatalf("privileged broker client failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.SelfTest(ctx); err != nil {
		log.Fatalf("privileged broker self-test failed: %v", err)
	}
	fmt.Println("privileged broker self-test passed")
}

func runAuth(args []string) {
	if len(args) == 0 {
		log.Fatalf("auth command required: status | setup-token new | recovery new")
	}

	switch args[0] {
	case "status":
		status := server.LoadAuthStatus()
		if status.PasswordConfigured {
			fmt.Println("password_configured=true")
		} else {
			fmt.Println("password_configured=false")
		}
		fmt.Printf("setup_token_issued=%t\n", status.SetupTokenIssued)
		fmt.Printf("recovery_token_issued=%t\n", status.RecoveryTokenIssued)
	case "setup-token":
		if len(args) < 2 || args[1] != "new" {
			log.Fatalf("usage: lightningos-manager auth setup-token new")
		}
		token, expiresAt, err := server.IssueSetupToken()
		if err != nil {
			log.Fatalf("setup token failed: %v", err)
		}
		fmt.Printf("setup_token=%s\n", token)
		fmt.Printf("expires_at=%s\n", expiresAt.Format(time.RFC3339))
	case "recovery":
		if len(args) < 2 || args[1] != "new" {
			log.Fatalf("usage: lightningos-manager auth recovery new")
		}
		token, expiresAt, err := server.IssueRecoveryToken()
		if err != nil {
			log.Fatalf("recovery token failed: %v", err)
		}
		fmt.Printf("recovery_token=%s\n", token)
		fmt.Printf("expires_at=%s\n", expiresAt.Format(time.RFC3339))
	default:
		log.Fatalf("unknown auth command: %s", args[0])
	}
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
	privilegedClient, err := privileged.NewClient(
		cfg.Privileged.Mode,
		time.Duration(cfg.Privileged.TimeoutSeconds)*time.Second,
		logger,
	)
	if err != nil {
		logger.Fatalf("privileged broker config failed: %v", err)
	}
	system.ConfigurePrivilegedClient(privilegedClient)
	if privilegedClient.Mode() != string(privileged.ModeDisabled) {
		selfTestCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Privileged.TimeoutSeconds)*time.Second)
		if err := privilegedClient.SelfTest(selfTestCtx); err != nil {
			logger.Printf("privileged broker %s self-test failed: %v", privilegedClient.Mode(), err)
		} else {
			logger.Printf("privileged broker %s self-test passed", privilegedClient.Mode())
		}
		cancel()
	}
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

	row, err := svc.RunDaily(ctx, reportDate, loc, nil, nil, nil, nil)
	if err != nil {
		logger.Fatalf("reports-run failed: %v", err)
	}
	if _, err := svc.CaptureMovementTargetForDate(ctx, time.Now().In(loc), loc); err != nil {
		logger.Printf("reports: movement target capture failed: %v", err)
	}

	logger.Printf(
		"reports: stored %s (revenue %d sats, offchain cost %d sats, onchain cost %d sats, total cost %d sats, net %d sats)",
		row.ReportDate.Format("2006-01-02"),
		row.Metrics.ForwardFeeRevenueSat,
		row.Metrics.TotalFeeCostSat(),
		row.Metrics.OnchainFeeCostSat,
		row.Metrics.TotalFeeCostWithOnchainSat(),
		row.Metrics.NetRoutingProfitSat,
	)
}

func runReportsBackfill(args []string) {
	fs := flag.NewFlagSet("reports-backfill", flag.ExitOnError)
	configPath := fs.String("config", "/etc/lightningos/config.yaml", "Path to config.yaml")
	fromStr := fs.String("from", "", "Start date (YYYY-MM-DD)")
	toStr := fs.String("to", "", "End date (YYYY-MM-DD)")
	maxDays := fs.Int("max-days", 0, "Override max range in days (0 uses default limit)")
	dryRun := fs.Bool("dry-run", false, "Recompute and compare against the stored rows without writing anything")
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
	paymentByDay, err := reports.FetchPaymentFeesByDay(context.Background(), lnd, uint64(startLocal.UTC().Unix()), uint64(endLocal.UTC().Unix()), loc)
	if err != nil {
		logger.Fatalf("reports-backfill failed: %v", err)
	}
	keysendByDay, err := reports.FetchKeysendReceivedByDay(context.Background(), lnd, uint64(startLocal.UTC().Unix()), uint64(endLocal.UTC().Unix()), loc)
	if err != nil {
		logger.Fatalf("reports-backfill failed: %v", err)
	}
	onchainByDay, err := reports.FetchOnchainFeesByDay(context.Background(), lnd, uint64(startLocal.UTC().Unix()), uint64(endLocal.UTC().Unix()), loc)
	if err != nil {
		logger.Fatalf("reports-backfill failed: %v", err)
	}
	// A dry run answers one question before anything is overwritten: does the node
	// still hold the data the stored rows were built from? It compares the inputs
	// - forwards, rebalances, payments, on-chain - rather than the net, because a
	// changed formula moves the net on its own. A component that recomputes lower
	// than what is stored means LND has pruned that period, and recalculating it
	// would replace a real number with a smaller, wrong one.
	if *dryRun {
		stored, err := reports.FetchRange(context.Background(), pool, startDate, endDate)
		if err != nil {
			logger.Fatalf("reports-backfill dry run failed: %v", err)
		}
		storedByDay := make(map[string]reports.Row, len(stored))
		for _, row := range stored {
			storedByDay[row.ReportDate.Format("2006-01-02")] = row
		}
		logger.Printf("reports: DRY RUN - nothing will be written")
		logger.Printf("%-12s %-10s %12s %12s %s", "date", "component", "stored", "recomputed", "verdict")
		suspect := 0
		for day := startDate; !day.After(endDate); day = day.AddDate(0, 0, 1) {
			dayCtx, dayCancel := context.WithTimeout(context.Background(), reportsRunTimeout())
			dayKey := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
			rebalanceOverride := rebalanceByDay[dayKey]
			paymentOverride := paymentByDay[dayKey]
			keysendOverride := keysendByDay[dayKey]
			onchainOverride := onchainByDay[dayKey]
			tr := reports.BuildTimeRangeForDate(day, loc)
			fresh, err := reports.ComputeMetrics(dayCtx, lnd, tr, false,
				&rebalanceOverride, &paymentOverride, &keysendOverride, &onchainOverride)
			dayCancel()
			if err != nil {
				logger.Fatalf("reports-backfill dry run failed on %s: %v", day.Format("2006-01-02"), err)
			}
			label := day.Format("2006-01-02")
			old, ok := storedByDay[label]
			if !ok {
				logger.Printf("%-12s %-10s %12s %12s %s", label, "-", "(no row)", "-", "nothing stored yet")
				continue
			}
			for _, c := range []struct {
				name             string
				stored, computed int64
			}{
				{"forwards", old.Metrics.ForwardFeeRevenueSat, fresh.ForwardFeeRevenueSat},
				{"rebalances", old.Metrics.RebalanceFeeCostSat, fresh.RebalanceFeeCostSat},
				{"payments", old.Metrics.PaymentFeeCostSat, fresh.PaymentFeeCostSat},
				{"onchain", old.Metrics.OnchainFeeCostSat, fresh.OnchainFeeCostSat},
				{"keysend", old.Metrics.KeysendReceivedSat, fresh.KeysendReceivedSat},
			} {
				verdict := reports.CompareStoredComponent(c.stored, c.computed)
				if !verdict.SafeToRecalculate() {
					suspect++
				}
				if verdict != reports.DryRunMatches {
					logger.Printf("%-12s %-10s %12d %12d %s", label, c.name, c.stored, c.computed, verdict)
				}
			}
		}
		if suspect == 0 {
			logger.Printf("reports: dry run clean - every component recomputes at or above the stored value")
		} else {
			logger.Printf("reports: dry run found %d component(s) recomputing LOWER than stored; recalculating would lose data", suspect)
		}
		return
	}

	for day := startDate; !day.After(endDate); day = day.AddDate(0, 0, 1) {
		dayCtx, dayCancel := context.WithTimeout(context.Background(), reportsRunTimeout())
		dayKey := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
		rebalanceOverride := rebalanceByDay[dayKey]
		paymentOverride := paymentByDay[dayKey]
		keysendOverride := keysendByDay[dayKey]
		onchainOverride := onchainByDay[dayKey]
		row, err := svc.RunDaily(dayCtx, day, loc, &rebalanceOverride, &paymentOverride, &keysendOverride, &onchainOverride)
		dayCancel()
		if err != nil {
			logger.Fatalf("reports-backfill failed on %s: %v", day.Format("2006-01-02"), err)
		}
		logger.Printf(
			"reports: stored %s (revenue %d sats, offchain cost %d sats, onchain cost %d sats, total cost %d sats, net %d sats)",
			row.ReportDate.Format("2006-01-02"),
			row.Metrics.ForwardFeeRevenueSat,
			row.Metrics.TotalFeeCostSat(),
			row.Metrics.OnchainFeeCostSat,
			row.Metrics.TotalFeeCostWithOnchainSat(),
			row.Metrics.NetRoutingProfitSat,
		)
	}
}

func runReportsReconcile(args []string) {
	fs := flag.NewFlagSet("reports-reconcile", flag.ExitOnError)
	configPath := fs.String("config", "/etc/lightningos/config.yaml", "Path to config.yaml")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config load failed: %v", err)
	}
	logger := log.New(os.Stdout, "", log.LstdFlags)
	dsn, err := server.ResolveNotificationsDSN(logger)
	if err != nil {
		logger.Fatalf("reports-reconcile failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), reportsRunTimeout())
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		logger.Fatalf("reports-reconcile failed: %v", err)
	}
	defer pool.Close()

	svc := reports.NewService(pool, lndclient.New(cfg, logger), logger)
	if err := svc.EnsureSchema(ctx); err != nil {
		logger.Fatalf("reports-reconcile failed: %v", err)
	}
	loc := resolveReportsLocation(logger)
	yesterday := time.Now().In(loc).AddDate(0, 0, -1)
	missing, err := svc.MissingDailyDates(ctx, yesterday)
	if err != nil {
		logger.Fatalf("reports-reconcile failed: %v", err)
	}
	if len(missing) == 0 {
		logger.Printf("reports: reconciliation complete; no missing daily reports")
		return
	}
	logger.Printf("reports: reconciling %d missing daily report(s), %s -> %s", len(missing), missing[0].Format("2006-01-02"), missing[len(missing)-1].Format("2006-01-02"))
	if err := svc.ReconcileDates(ctx, missing, loc, func(completed, total int, reportDate time.Time) {
		logger.Printf("reports: reconciled %s (%d/%d)", reportDate.Format("2006-01-02"), completed, total)
	}); err != nil {
		logger.Fatalf("reports-reconcile failed: %v", err)
	}
	if _, err := svc.CaptureMovementTargetForDate(ctx, time.Now().In(loc), loc); err != nil {
		logger.Printf("reports: movement target capture failed: %v", err)
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
