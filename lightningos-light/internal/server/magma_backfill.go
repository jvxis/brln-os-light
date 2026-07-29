package server

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"lightningos-light/lnrpc"

	"google.golang.org/grpc"
)

// Historical sales predate this app: they were closed by a standalone script and
// carry no revenue_settled_at, so they contribute nothing to the reports. Their
// channel-open fees, however, were always counted by the generic on-chain scan.
// Those past days therefore under-report profit, and this is the repair.
//
// The date has to come from the node, not from Amboss: an order's updated_at is
// when channel monitoring ended, often months after the buyer paid. LND knows the
// exact settle time, and the invoices are findable because the original script
// used the same structured memo this app uses.

const (
	magmaInvoiceMemoPrefix = "Magma-Channel-Sale-Order-ID:"
	magmaScanPageSize      = 5000
	magmaScanMaxPages      = 400
)

// magmaHistoricalInvoice is one settled sale invoice found on the node.
type magmaHistoricalInvoice struct {
	OrderID    string
	AmountSat  int64
	SettledAt  time.Time
	KnownOrder bool
}

// MagmaBackfillReport is the dry run. Nothing is written until Apply is called.
type MagmaBackfillReport struct {
	InvoicesFound    int        `json:"invoices_found"`
	Matched          int        `json:"matched_orders"`
	AlreadyStamped   int        `json:"already_stamped"`
	Unmatched        int        `json:"unmatched_invoices"`
	RevenueSat       int64      `json:"revenue_sat"`
	OldestSettledAt  *time.Time `json:"oldest_settled_at,omitempty"`
	NewestSettledAt  *time.Time `json:"newest_settled_at,omitempty"`
	Applied          bool       `json:"applied"`
	Stamped          int        `json:"stamped"`
	ReportsRerunFrom string     `json:"reports_rerun_from,omitempty"`
	ReportsRerunTo   string     `json:"reports_rerun_to,omitempty"`
	Notes            []string   `json:"notes,omitempty"`
}

type magmaInvoiceScanner interface {
	BorrowLightning(ctx context.Context, stream bool) (*grpc.ClientConn, func(), error)
}

// scanHistoricalInvoices walks settled invoices looking for the sale memo. It
// reads the node directly rather than going through lndclient so no shared
// helper has to grow a Magma-specific concept.
func (s *MagmaService) scanHistoricalInvoices(ctx context.Context) ([]magmaHistoricalInvoice, error) {
	scanner, ok := s.lnd.(magmaInvoiceScanner)
	if !ok {
		return nil, errors.New("LND client cannot list invoices")
	}
	conn, release, err := scanner.BorrowLightning(ctx, false)
	if err != nil {
		return nil, err
	}
	defer release()

	client := lnrpc.NewLightningClient(conn)
	found := make([]magmaHistoricalInvoice, 0, 64)
	var indexOffset uint64

	for page := 0; page < magmaScanMaxPages; page++ {
		resp, err := client.ListInvoices(ctx, &lnrpc.ListInvoiceRequest{
			Reversed:       true,
			IndexOffset:    indexOffset,
			NumMaxInvoices: magmaScanPageSize,
		})
		if err != nil {
			return nil, err
		}
		if resp == nil || len(resp.Invoices) == 0 {
			break
		}
		for _, inv := range resp.Invoices {
			if inv == nil || inv.State != lnrpc.Invoice_SETTLED {
				continue
			}
			orderID := magmaOrderIDFromMemo(inv.Memo)
			if orderID == "" {
				continue
			}
			settledAt := inv.SettleDate
			if settledAt == 0 {
				settledAt = inv.CreationDate
			}
			if settledAt == 0 {
				continue
			}
			amount := inv.AmtPaidSat
			if amount == 0 {
				amount = inv.Value
			}
			found = append(found, magmaHistoricalInvoice{
				OrderID:   orderID,
				AmountSat: amount,
				SettledAt: time.Unix(settledAt, 0).UTC(),
			})
		}
		if resp.FirstIndexOffset == 0 {
			break
		}
		indexOffset = resp.FirstIndexOffset
	}
	return found, nil
}

// magmaOrderIDFromMemo extracts the order id from the structured memo the sale
// invoice carries. Anything else is somebody else's invoice.
func magmaOrderIDFromMemo(memo string) string {
	trimmed := strings.TrimSpace(memo)
	if !strings.HasPrefix(trimmed, magmaInvoiceMemoPrefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(trimmed, magmaInvoiceMemoPrefix))
}

// BackfillRevenue reconciles historical sale invoices against the order table.
// With apply=false nothing is written; that is the default and the only way to
// see what would change.
func (s *MagmaService) BackfillRevenue(ctx context.Context, apply bool) (MagmaBackfillReport, error) {
	invoices, err := s.scanHistoricalInvoices(ctx)
	if err != nil {
		return MagmaBackfillReport{}, err
	}

	report := MagmaBackfillReport{InvoicesFound: len(invoices), Applied: apply}
	if len(invoices) == 0 {
		report.Notes = append(report.Notes,
			"no sale invoices found on the node; the historical invoices are gone or this node never issued them")
		return report, nil
	}

	// Which orders we know about, and which already carry a date.
	known := make(map[string]bool, 128)
	stamped := make(map[string]bool, 128)
	rows, err := s.db.Query(ctx, `select order_id, revenue_settled_at is not null from magma_orders`)
	if err != nil {
		return MagmaBackfillReport{}, err
	}
	for rows.Next() {
		var id string
		var hasDate bool
		if err := rows.Scan(&id, &hasDate); err == nil {
			known[id] = true
			stamped[id] = hasDate
		}
	}
	rows.Close()

	// One invoice per order is the norm, but a re-issued invoice would produce
	// two. The earliest settle is the one that represents the sale.
	earliest := make(map[string]magmaHistoricalInvoice, len(invoices))
	for _, inv := range invoices {
		current, seen := earliest[inv.OrderID]
		if !seen || inv.SettledAt.Before(current.SettledAt) {
			earliest[inv.OrderID] = inv
		}
	}

	pending := make([]magmaHistoricalInvoice, 0, len(earliest))
	for orderID, inv := range earliest {
		if !known[orderID] {
			report.Unmatched++
			continue
		}
		if stamped[orderID] {
			report.AlreadyStamped++
			continue
		}
		inv.KnownOrder = true
		pending = append(pending, inv)
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].SettledAt.Before(pending[j].SettledAt) })

	for _, inv := range pending {
		report.Matched++
		report.RevenueSat += inv.AmountSat
	}
	if len(pending) > 0 {
		oldest := pending[0].SettledAt
		newest := pending[len(pending)-1].SettledAt
		report.OldestSettledAt = &oldest
		report.NewestSettledAt = &newest
		report.ReportsRerunFrom = oldest.Format("2006-01-02")
		report.ReportsRerunTo = newest.Format("2006-01-02")
	}
	if report.Unmatched > 0 {
		report.Notes = append(report.Notes, fmt.Sprintf(
			"%d sale invoice(s) have no matching order in this app; they are ignored", report.Unmatched))
	}

	if !apply || len(pending) == 0 {
		return report, nil
	}

	for _, inv := range pending {
		// Guarded on null so a re-run cannot move a date that already exists.
		tag, err := s.db.Exec(ctx, `
update magma_orders set revenue_settled_at=$2, updated_at=now()
where order_id=$1 and revenue_settled_at is null
`, inv.OrderID, inv.SettledAt)
		if err != nil {
			return report, err
		}
		if tag.RowsAffected() > 0 {
			report.Stamped++
		}
	}
	s.appendEventGlobal(ctx, fmt.Sprintf(
		"Backfilled %d historical sale(s) totalling %s sats, settled between %s and %s",
		report.Stamped, formatInt(report.RevenueSat), report.ReportsRerunFrom, report.ReportsRerunTo))
	return report, nil
}

// appendEventGlobal records an action that is not tied to a single order. It
// attaches to the oldest affected order so the audit trail has a home without
// inventing a synthetic row.
func (s *MagmaService) appendEventGlobal(ctx context.Context, message string) {
	var orderID string
	if err := s.db.QueryRow(ctx,
		`select order_id from magma_orders order by coalesce(order_created_at, first_seen_at) asc limit 1`,
	).Scan(&orderID); err != nil || orderID == "" {
		if s.logger != nil {
			s.logger.Printf("magma: %s", message)
		}
		return
	}
	s.appendEvent(ctx, orderID, "backfill", "info", message, nil)
}
