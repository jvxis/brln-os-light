package reports

import (
	"context"
	"fmt"
	"time"

	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"
)

const paymentScanPageSize = 5000
const paymentScanMaxPages = 200000

type OutgoingPaymentMetrics struct {
	Payments   PaymentOverride
	Rebalances RebalanceOverride
}

func FetchOutgoingPaymentMetrics(ctx context.Context, lnd *lndclient.Client, startUnix uint64, endUnix uint64, memoMatch bool) (OutgoingPaymentMetrics, error) {
	totals := OutgoingPaymentMetrics{}
	err := scanOutgoingPayments(ctx, lnd, startUnix, endUnix, memoMatch, func(_ int64, feeMsat int64, amountMsat int64, isRebalance bool) {
		if isRebalance {
			totals.Rebalances.FeeMsat += feeMsat
			totals.Rebalances.Count++
			totals.Rebalances.AmountMsat += amountMsat
			return
		}
		totals.Payments.FeeMsat += feeMsat
		totals.Payments.Count++
	})
	if err != nil {
		return OutgoingPaymentMetrics{}, err
	}
	return totals, nil
}

func FetchPaymentMetrics(ctx context.Context, lnd *lndclient.Client, startUnix uint64, endUnix uint64, memoMatch bool) (PaymentOverride, error) {
	totals := PaymentOverride{}
	err := scanOutgoingPayments(ctx, lnd, startUnix, endUnix, memoMatch, func(_ int64, feeMsat int64, _ int64, isRebalance bool) {
		if isRebalance {
			return
		}
		totals.FeeMsat += feeMsat
		totals.Count++
	})
	if err != nil {
		return PaymentOverride{}, err
	}
	return totals, nil
}

func FetchPaymentFeesByDay(ctx context.Context, lnd *lndclient.Client, startUnix uint64, endUnix uint64, loc *time.Location) (map[time.Time]PaymentOverride, error) {
	if lnd == nil {
		return nil, fmt.Errorf("lnd client unavailable")
	}
	if loc == nil {
		loc = time.Local
	}

	results := make(map[time.Time]PaymentOverride)
	err := scanOutgoingPayments(ctx, lnd, startUnix, endUnix, false, func(ts int64, feeMsat int64, _ int64, isRebalance bool) {
		if isRebalance {
			return
		}
		local := time.Unix(ts, 0).In(loc)
		dayKey := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
		current := results[dayKey]
		current.FeeMsat += feeMsat
		current.Count++
		results[dayKey] = current
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func scanOutgoingPayments(ctx context.Context, lnd *lndclient.Client, startUnix uint64, endUnix uint64, memoMatch bool, onMatch func(ts int64, feeMsat int64, amountMsat int64, isRebalance bool)) error {
	if lnd == nil {
		return fmt.Errorf("lnd client unavailable")
	}

	pubkey, err := fetchNodePubkey(ctx, lnd)
	if err != nil {
		return err
	}

	conn, err := lnd.DialLightning(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	decodeCache := map[string]decodedPayReq{}

	var indexOffset uint64
	var pages int
	var lastOffset uint64

	for {
		if pages >= paymentScanMaxPages {
			break
		}
		pages++

		req := &lnrpc.ListPaymentsRequest{
			IncludeIncomplete: false,
			Reversed:          true,
			IndexOffset:       indexOffset,
			MaxPayments:       paymentScanPageSize,
			CreationDateStart: startUnix,
			CreationDateEnd:   endUnix,
		}
		resp, err := client.ListPayments(ctx, req)
		if err != nil {
			return err
		}
		if resp == nil || len(resp.Payments) == 0 {
			break
		}

		minIndex := uint64(0)
		nextOffset := uint64(0)
		maxTs := int64(0)
		minTs := int64(1<<63 - 1)

		for _, pay := range resp.Payments {
			if pay == nil {
				continue
			}
			if pay.PaymentIndex > 0 {
				if minIndex == 0 || pay.PaymentIndex < minIndex {
					minIndex = pay.PaymentIndex
				}
			}

			ts := extractPaymentTimestamp(pay)
			if ts > maxTs {
				maxTs = ts
			}
			if ts < minTs {
				minTs = ts
			}

			if ts < int64(startUnix) || ts > int64(endUnix) {
				continue
			}
			if !PaymentSucceeded(pay) {
				continue
			}

			dest := ""
			description := ""
			if memoMatch {
				dest, description = extractDestinationAndDescription(ctx, lnd, pay, decodeCache)
			}
			isRebalance := IsRebalancePayment(pay, pubkey, dest, description, memoMatch)
			feeMsat := extractPaymentFeeMsat(pay)
			amountMsat := extractPaymentValueMsat(pay)
			if onMatch != nil {
				onMatch(ts, feeMsat, amountMsat, isRebalance)
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

		if len(resp.Payments) < paymentScanPageSize && minTs < int64(startUnix) {
			break
		}
	}

	return nil
}
