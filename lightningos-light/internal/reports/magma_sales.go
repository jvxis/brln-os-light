package reports

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Channel sales are revenue the node earns without routing anything: the buyer
// pays a plain bolt11 invoice, which no other scanner in this package looks at
// (the keysend scan skips non-keysend invoices by design). The on-chain fee of
// the funding transaction, on the other hand, is already picked up by the
// generic on-chain scan.
//
// So only the revenue side is read here. Attributing the cost as well would
// count the same fee twice.
//
// The dependency points this way on purpose: the Magma app owns the data,
// because only it can tell a settled invoice was a channel sale. This package
// just asks, and asks only when the app is actually installed.

// MagmaSalesRevenue is the sales contribution over a window.
type MagmaSalesRevenue struct {
	RevenueMsat int64
	Count       int64
}

// FetchMagmaSalesRevenue returns the revenue realised in [start, end). The bool
// reports whether the Magma app is present and installed; when it is not, the
// caller must leave the metrics untouched rather than record a zero.
func FetchMagmaSalesRevenue(ctx context.Context, db *pgxpool.Pool, start, end time.Time) (MagmaSalesRevenue, bool, error) {
	if db == nil {
		return MagmaSalesRevenue{}, false, nil
	}

	var tablesExist bool
	if err := db.QueryRow(ctx, `
select to_regclass('magma_orders') is not null and to_regclass('magma_settings') is not null
`).Scan(&tablesExist); err != nil {
		return MagmaSalesRevenue{}, false, err
	}
	if !tablesExist {
		return MagmaSalesRevenue{}, false, nil
	}

	var installed bool
	if err := db.QueryRow(ctx,
		`select coalesce((select app_installed from magma_settings where id=1), false)`,
	).Scan(&installed); err != nil {
		return MagmaSalesRevenue{}, false, err
	}
	if !installed {
		return MagmaSalesRevenue{}, false, nil
	}

	// revenue_settled_at is stamped only when the app observes the buyer's
	// payment land, so orders imported from history carry no timestamp and
	// cannot leak into a report for the day they were first seen.
	var revenueSat, count int64
	if err := db.QueryRow(ctx, `
select coalesce(sum(revenue_sat), 0), count(*)
from magma_orders
where revenue_settled_at >= $1 and revenue_settled_at < $2
`, start, end).Scan(&revenueSat, &count); err != nil {
		return MagmaSalesRevenue{}, false, err
	}

	return MagmaSalesRevenue{RevenueMsat: revenueSat * 1000, Count: count}, true, nil
}
