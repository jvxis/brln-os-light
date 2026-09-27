package reports

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Serializes report/mark writes only, not LND collection or historical reads.
// Separate from reconciliation's lock, which is held while collecting from LND.
const derivedWriteLock int64 = 0x4c4f534445524956

type reportQuery interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func lockDerivedWrites(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", derivedWriteLock)
	return err
}

// SQL DATE is a calendar label, not a UTC instant.
func reportDayRange(day time.Time, loc *time.Location) TimeRange {
	if loc == nil {
		loc = time.Local
	}
	return BuildTimeRangeForDate(time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc), loc)
}

// One indexed, batched mark query for the whole range, including custom/fixed
// timezones and DST days. No query per day and no dependency on PostgreSQL's
// timezone names matching Go's "Local" or FixedZone names.
func deriveRows(ctx context.Context, db reportQuery, items []Row, loc *time.Location) error {
	if len(items) == 0 {
		return nil
	}
	exists, err := activityMarksExist(ctx, db)
	if err != nil {
		return err
	}
	marks := make([]ActivityMarkTotals, len(items))
	if exists {
		starts, ends := make([]time.Time, len(items)), make([]time.Time, len(items))
		for i, row := range items {
			tr := reportDayRange(row.ReportDate, loc)
			starts[i], ends[i] = tr.StartUTC, tr.EndUTC
		}
		rows, err := db.Query(ctx, `
select d.ordinality, m.classification, sum(m.amount_msat), count(*)
from unnest($1::timestamptz[], $2::timestamptz[]) with ordinality as d(start_at,end_at,ordinality)
join report_activity_marks m on m.occurred_at >= d.start_at and m.occurred_at < d.end_at
group by d.ordinality,m.classification`, starts, ends)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var index, amount, count int64
			var classification string
			if err := rows.Scan(&index, &classification, &amount, &count); err != nil {
				return err
			}
			if index < 1 || index > int64(len(marks)) {
				return errors.New("invalid activity mark day")
			}
			switch classification {
			case MarkRevenue:
				marks[index-1].RevenueMsat = amount
				marks[index-1].RevenueUnit = count
			case MarkCost:
				marks[index-1].CostMsat = amount
				marks[index-1].CostUnit = count
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}
	for i := range items {
		items[i].Metrics = items[i].Metrics.WithActivityMarks(marks[i]).WithDerivedTotals()
	}
	return nil
}

func (s *Service) historicalRows(ctx context.Context, start, end time.Time, all bool, loc *time.Location) ([]Row, error) {
	if s.db == nil {
		return nil, nil
	}
	// Components and classifications must describe the same database snapshot.
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	var items []Row
	if all {
		items, err = fetchAll(ctx, tx)
	} else {
		items, err = fetchRange(ctx, tx, start, end)
	}
	if err != nil {
		return nil, err
	}
	if err = deriveRows(ctx, tx, items, loc); err != nil {
		return nil, err
	}
	return items, tx.Commit(ctx)
}

func upsertDailyWithMarks(ctx context.Context, db *pgxpool.Pool, row Row, loc *time.Location) (Row, error) {
	if db == nil {
		row.Metrics = row.Metrics.WithDerivedTotals()
		return row, nil
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return Row{}, err
	}
	defer tx.Rollback(context.Background())
	if err = lockDerivedWrites(ctx, tx); err != nil {
		return Row{}, err
	}
	items := []Row{row}
	if err = deriveRows(ctx, tx, items, loc); err != nil {
		return Row{}, err
	}
	// Persist ORIGINAL source metrics, not sat-only normalization of the read copy.
	// Marks are stored separately; only their contribution to net is materialized.
	assignDerived(&row.Metrics, items[0].Metrics)
	query, args := buildUpsertDaily(row)
	if _, err = tx.Exec(ctx, query, args...); err != nil {
		return Row{}, err
	}
	return items[0], tx.Commit(ctx)
}

func assignDerived(dst *Metrics, src Metrics) {
	dst.NetRoutingProfitSat = src.NetRoutingProfitSat
	dst.NetRoutingProfitMsat = src.NetRoutingProfitMsat
	dst.NetWithKeysendSat = src.NetWithKeysendSat
	dst.NetWithKeysendMsat = src.NetWithKeysendMsat
	dst.NetTotalSat = src.NetTotalSat
	dst.NetTotalMsat = src.NetTotalMsat
}

func sameDerived(a, b Metrics) bool {
	return a.NetRoutingProfitSat == b.NetRoutingProfitSat && a.NetRoutingProfitMsat == b.NetRoutingProfitMsat &&
		a.NetWithKeysendSat == b.NetWithKeysendSat && a.NetWithKeysendMsat == b.NetWithKeysendMsat &&
		a.NetTotalSat == b.NetTotalSat && a.NetTotalMsat == b.NetTotalMsat
}

func updateDerived(ctx context.Context, tx pgx.Tx, row Row) error {
	m := row.Metrics
	tag, err := tx.Exec(ctx, `
update reports_daily set net_routing_profit_sats=$2,net_routing_profit_msat=$3,
net_with_keysend_sats=$4,net_with_keysend_msat=$5,net_total_sats=$6,net_total_msat=$7,
updated_at=now() where report_date=$1`, normalizeReportDate(row.ReportDate),
		m.NetRoutingProfitSat, m.NetRoutingProfitMsat, m.NetWithKeysendSat, m.NetWithKeysendMsat, m.NetTotalSat, m.NetTotalMsat)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("daily report changed during derived repair")
	}
	return nil
}

type DerivedChange struct {
	Date   time.Time
	Before Metrics
	After  Metrics
}

// RepairDerived neither creates schema nor queries LND. Dry runs use a read-only
// snapshot; apply serializes with mark edits/daily writes and commits atomically.
func RepairDerived(ctx context.Context, db *pgxpool.Pool, start, end time.Time, loc *time.Location, dryRun bool) ([]DerivedChange, error) {
	if db == nil {
		return nil, errors.New("reports database unavailable")
	}
	options := pgx.TxOptions{}
	if dryRun {
		options = pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	}
	tx, err := db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	if !dryRun {
		if err = lockDerivedWrites(ctx, tx); err != nil {
			return nil, err
		}
	}
	items, err := fetchRange(ctx, tx, start, end, !dryRun)
	if err != nil {
		return nil, err
	}
	before := append([]Row(nil), items...)
	if err = deriveRows(ctx, tx, items, loc); err != nil {
		return nil, err
	}
	changes := []DerivedChange{}
	for i, row := range items {
		if sameDerived(before[i].Metrics, row.Metrics) {
			continue
		}
		changes = append(changes, DerivedChange{Date: row.ReportDate, Before: before[i].Metrics, After: row.Metrics})
		if !dryRun {
			if err = updateDerived(ctx, tx, row); err != nil {
				return nil, err
			}
		}
	}
	return changes, tx.Commit(ctx)
}
