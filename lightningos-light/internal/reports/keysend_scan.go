package reports

import (
	"context"
	"fmt"
	"time"

	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"
)

const keysendScanPageSize = 5000
const keysendScanMaxPages = 200000

func FetchKeysendReceivedMetrics(ctx context.Context, lnd *lndclient.Client, startUnix uint64, endUnix uint64) (KeysendReceivedOverride, error) {
	totals := KeysendReceivedOverride{}
	err := scanIncomingKeysendInvoices(ctx, lnd, startUnix, endUnix, func(_ int64, amountMsat int64) {
		totals.AmountMsat += amountMsat
		totals.Count++
	})
	if err != nil {
		return KeysendReceivedOverride{}, err
	}
	return totals, nil
}

func FetchKeysendReceivedByDay(ctx context.Context, lnd *lndclient.Client, startUnix uint64, endUnix uint64, loc *time.Location) (map[time.Time]KeysendReceivedOverride, error) {
	if lnd == nil {
		return nil, fmt.Errorf("lnd client unavailable")
	}
	if loc == nil {
		loc = time.Local
	}

	results := make(map[time.Time]KeysendReceivedOverride)
	err := scanIncomingKeysendInvoices(ctx, lnd, startUnix, endUnix, func(ts int64, amountMsat int64) {
		local := time.Unix(ts, 0).In(loc)
		dayKey := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
		current := results[dayKey]
		current.AmountMsat += amountMsat
		current.Count++
		results[dayKey] = current
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func scanIncomingKeysendInvoices(ctx context.Context, lnd *lndclient.Client, startUnix uint64, endUnix uint64, onMatch func(ts int64, amountMsat int64)) error {
	if lnd == nil {
		return fmt.Errorf("lnd client unavailable")
	}

	conn, release, err := lnd.BorrowLightning(ctx, false)
	if err != nil {
		return err
	}
	defer release()

	client := lnrpc.NewLightningClient(conn)

	var indexOffset uint64
	var pages int
	var lastOffset uint64

	for {
		if pages >= keysendScanMaxPages {
			break
		}
		pages++

		req := &lnrpc.ListInvoiceRequest{
			PendingOnly:       false,
			Reversed:          true,
			IndexOffset:       indexOffset,
			NumMaxInvoices:    keysendScanPageSize,
			CreationDateStart: startUnix,
			CreationDateEnd:   endUnix,
		}
		resp, err := client.ListInvoices(ctx, req)
		if err != nil {
			return err
		}
		if resp == nil || len(resp.Invoices) == 0 {
			break
		}

		minIndex := uint64(0)
		nextOffset := uint64(0)
		maxTs := int64(0)
		minTs := int64(1<<63 - 1)

		for _, inv := range resp.Invoices {
			if inv == nil {
				continue
			}
			if inv.AddIndex > 0 {
				if minIndex == 0 || inv.AddIndex < minIndex {
					minIndex = inv.AddIndex
				}
			}

			ts := extractInvoiceTimestamp(inv)
			if ts > maxTs {
				maxTs = ts
			}
			if ts < minTs {
				minTs = ts
			}

			if ts < int64(startUnix) || ts > int64(endUnix) {
				continue
			}
			if inv.State != lnrpc.Invoice_SETTLED {
				continue
			}
			if !inv.IsKeysend {
				continue
			}
			amountMsat := extractInvoiceAmountMsat(inv)
			if onMatch != nil {
				onMatch(ts, amountMsat)
			}
		}

		if maxTs < int64(startUnix) {
			break
		}
		if resp.FirstIndexOffset != 0 {
			nextOffset = resp.FirstIndexOffset
		} else if minIndex != 0 {
			nextOffset = minIndex
		}
		if nextOffset == 0 {
			break
		}
		if nextOffset == indexOffset || lastOffset == nextOffset {
			break
		}
		lastOffset = nextOffset
		indexOffset = nextOffset

		if len(resp.Invoices) < keysendScanPageSize && minTs < int64(startUnix) {
			break
		}
	}

	return nil
}
