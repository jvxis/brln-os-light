package reports

import (
	"context"
	"errors"
	"sort"
	"time"
)

type ReconcileProgress func(completed, total int, reportDate time.Time)

var ErrReconciliationInProgress = errors.New("report reconciliation is already running")

const reportsReconciliationAdvisoryLock int64 = 0x4c4f535245504f52

func (s *Service) MissingDailyDates(ctx context.Context, endDate time.Time) ([]time.Time, error) {
	if s == nil {
		return nil, errors.New("reports service is unavailable")
	}
	return MissingDailyDates(ctx, s.db, endDate)
}

// ReconcileDates rebuilds only absent daily rows. The daily store uses an
// UPSERT, making retries safe if a prior attempt stopped partway through.
func (s *Service) ReconcileDates(ctx context.Context, dates []time.Time, loc *time.Location, progress ReconcileProgress) error {
	if s == nil || s.lnd == nil {
		return errors.New("reports service is unavailable")
	}
	if loc == nil {
		loc = time.Local
	}
	dates = normalizedUniqueDates(dates, loc)
	if len(dates) == 0 {
		return nil
	}
	if s.db == nil {
		return errors.New("reports database is unavailable")
	}
	lockTx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = lockTx.Rollback(context.Background()) }()
	var locked bool
	if err := lockTx.QueryRow(ctx, "select pg_try_advisory_xact_lock($1)", reportsReconciliationAdvisoryLock).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return ErrReconciliationInProgress
	}

	startDate := dates[0]
	endDate := dates[len(dates)-1]
	startLocal := time.Date(startDate.Year(), startDate.Month(), startDate.Day(), 0, 0, 0, 0, loc)
	endLocal := time.Date(endDate.Year(), endDate.Month(), endDate.Day(), 23, 59, 59, 0, loc)
	startUnix := uint64(startLocal.UTC().Unix())
	endUnix := uint64(endLocal.UTC().Unix())

	rebalanceByDay, err := FetchRebalanceFeesByDay(ctx, s.lnd, startUnix, endUnix, loc)
	if err != nil {
		return err
	}
	paymentByDay, err := FetchPaymentFeesByDay(ctx, s.lnd, startUnix, endUnix, loc)
	if err != nil {
		return err
	}
	keysendSentByDay, err := FetchKeysendSentByDay(ctx, s.lnd, startUnix, endUnix, loc)
	if err != nil {
		return err
	}
	keysendByDay, err := FetchKeysendReceivedByDay(ctx, s.lnd, startUnix, endUnix, loc)
	if err != nil {
		return err
	}
	onchainByDay, err := FetchOnchainFeesByDay(ctx, s.lnd, startUnix, endUnix, loc)
	if err != nil {
		return err
	}

	for index, day := range dates {
		if err := ctx.Err(); err != nil {
			return err
		}
		dayKey := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
		rebalanceOverride := rebalanceByDay[dayKey]
		paymentOverride := paymentByDay[dayKey]
		keysendOverride := keysendByDay[dayKey]
		keysendSentOverride := keysendSentByDay[dayKey]
		onchainOverride := onchainByDay[dayKey]
		if _, err := s.RunDaily(ctx, day, loc, &rebalanceOverride, &paymentOverride, &keysendOverride, &keysendSentOverride, &onchainOverride); err != nil {
			return err
		}
		if progress != nil {
			progress(index+1, len(dates), day)
		}
	}
	return nil
}

func normalizedUniqueDates(dates []time.Time, loc *time.Location) []time.Time {
	if loc == nil {
		loc = time.Local
	}
	unique := make(map[string]time.Time, len(dates))
	for _, value := range dates {
		// SQL DATE values have calendar semantics, not instant semantics. Preserve
		// their year/month/day when assigning the reports timezone so UTC midnight
		// does not become the preceding date west of UTC.
		day := time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, loc)
		unique[day.Format("2006-01-02")] = day
	}
	result := make([]time.Time, 0, len(unique))
	for _, day := range unique {
		result = append(result, day)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Before(result[j]) })
	return result
}
