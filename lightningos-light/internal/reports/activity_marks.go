package reports

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The technical shape of a Lightning payment does not reveal its economic
// purpose. An invoice the operator paid might be a coffee or the purchase of a
// channel; one that arrived might be a sale or a friend settling a debt. Only
// the operator knows, so only the operator can say.
//
// Marks are that answer. An unmarked payment stays out of the report entirely:
// the report measures operating performance, not the wallet statement, and the
// principal of a payment only becomes revenue or cost once someone classifies it.
//
// The fee is deliberately not part of this. Every payment fee is already counted
// under costs, marked or not; adding it again here would charge the same sats
// twice. A mark contributes the principal alone.

const (
	MarkRevenue = "revenue"
	MarkCost    = "cost"
)

const activityMarkSchema = `
create table if not exists report_activity_marks (
  payment_hash text primary key,
  classification text not null,
  amount_msat bigint not null default 0,
  occurred_at timestamptz not null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create index if not exists report_activity_marks_occurred_idx
  on report_activity_marks (occurred_at);`

// ActivityMarkTotals is what the marks contribute to a period.
type ActivityMarkTotals struct {
	RevenueMsat int64
	RevenueUnit int64
	CostMsat    int64
	CostUnit    int64
}

func EnsureActivityMarkSchema(ctx context.Context, db *pgxpool.Pool) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(ctx, activityMarkSchema)
	return err
}

func ValidMarkClassification(value string) bool {
	switch strings.TrimSpace(value) {
	case MarkRevenue, MarkCost:
		return true
	default:
		return false
	}
}

// SetActivityMark records or clears the operator's classification. An empty
// classification removes the mark, which is how a mistake is undone.
func SetActivityMark(ctx context.Context, db *pgxpool.Pool, paymentHash, classification string, amountMsat int64, occurredAt time.Time, locations ...*time.Location) error {
	hash := strings.TrimSpace(paymentHash)
	if hash == "" {
		return fmt.Errorf("payment hash required")
	}
	if db == nil {
		return fmt.Errorf("database unavailable")
	}
	classification = strings.TrimSpace(classification)
	if classification != "" && !ValidMarkClassification(classification) {
		return fmt.Errorf("classification must be %q or %q", MarkRevenue, MarkCost)
	}
	if amountMsat < 0 {
		return fmt.Errorf("amount must be zero or positive")
	}
	if classification != "" && occurredAt.IsZero() {
		return fmt.Errorf("the payment timestamp is required so the mark lands on the right day")
	}
	loc := time.Local
	if len(locations) > 0 && locations[0] != nil {
		loc = locations[0]
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = lockDerivedWrites(ctx, tx); err != nil {
		return err
	}
	var oldTime time.Time
	err = tx.QueryRow(ctx, "select occurred_at from report_activity_marks where payment_hash=$1", hash).Scan(&oldTime)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if classification == "" {
		_, err = tx.Exec(ctx, "delete from report_activity_marks where payment_hash=$1", hash)
	} else {
		_, err = tx.Exec(ctx, `
insert into report_activity_marks (payment_hash, classification, amount_msat, occurred_at)
values ($1,$2,$3,$4)
on conflict (payment_hash) do update set
  classification = excluded.classification,
  amount_msat = excluded.amount_msat,
  occurred_at = excluded.occurred_at,
  updated_at = now()`, hash, classification, amountMsat, occurredAt.UTC())
	}
	if err != nil {
		return err
	}
	dates := map[string]time.Time{}
	stamps := []time.Time{oldTime}
	if classification != "" {
		stamps = append(stamps, occurredAt)
	}
	for _, stamp := range stamps {
		if stamp.IsZero() {
			continue
		}
		day := dateOnly(stamp, loc)
		dates[day.Format("2006-01-02")] = day
	}
	for _, day := range dates {
		items, err := fetchRange(ctx, tx, day, day, true)
		if err != nil {
			return err
		}
		// A mark must not fabricate a historical row without source data.
		if err = deriveRows(ctx, tx, items, loc); err != nil {
			return err
		}
		for _, row := range items {
			if err = updateDerived(ctx, tx, row); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

// ListActivityMarks returns the classification for a set of payment hashes, so
// the wallet can render what is already marked without a query per row.
func ListActivityMarks(ctx context.Context, db *pgxpool.Pool, hashes []string) (map[string]string, error) {
	marks := make(map[string]string, len(hashes))
	if db == nil || len(hashes) == 0 {
		return marks, nil
	}
	rows, err := db.Query(ctx,
		`select payment_hash, classification from report_activity_marks where payment_hash = any($1)`, hashes)
	if err != nil {
		return marks, err
	}
	defer rows.Close()
	for rows.Next() {
		var hash, classification string
		if err := rows.Scan(&hash, &classification); err != nil {
			continue
		}
		marks[hash] = classification
	}
	return marks, rows.Err()
}

// FetchActivityMarkTotals sums the marks that fall inside a period.
func FetchActivityMarkTotals(ctx context.Context, db *pgxpool.Pool, start, end time.Time) (ActivityMarkTotals, error) {
	var totals ActivityMarkTotals
	if db == nil {
		return totals, nil
	}
	exists, err := activityMarksExist(ctx, db)
	if err != nil || !exists {
		return totals, err
	}
	rows, err := db.Query(ctx, `
select classification, coalesce(sum(amount_msat), 0), count(*)
from report_activity_marks
where occurred_at >= $1 and occurred_at < $2
group by classification`, start, end)
	if err != nil {
		return totals, err
	}
	defer rows.Close()
	for rows.Next() {
		var classification string
		var amount, count int64
		if err := rows.Scan(&classification, &amount, &count); err != nil {
			return totals, err
		}
		switch classification {
		case MarkRevenue:
			totals.RevenueMsat = amount
			totals.RevenueUnit = count
		case MarkCost:
			totals.CostMsat = amount
			totals.CostUnit = count
		}
	}
	return totals, rows.Err()
}

func activityMarksExist(ctx context.Context, db reportQuery) (bool, error) {
	var exists bool
	err := db.QueryRow(ctx, "select to_regclass('report_activity_marks') is not null").Scan(&exists)
	return exists, err
}
