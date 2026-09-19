package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"lightningos-light/lnrpc"
)

const invoiceReconcileLimit = 500

// The scalar index remains available to older managers. This checkpoint is the
// authoritative cursor for this manager, including the fence against invoices
// from a previous (larger) LND settlement sequence.
type invoiceCheckpoint struct {
	Index      uint64 `json:"index"`
	AddIndex   uint64 `json:"add_index"`
	SettledAt  int64  `json:"settled_at"`
	Hash       string `json:"hash"`
	EpochAfter int64  `json:"epoch_after,omitempty"`
}

func decodeInvoiceCheckpoint(raw string, legacy uint64) (invoiceCheckpoint, error) {
	if raw == "" {
		return invoiceCheckpoint{Index: legacy}, nil
	}
	var checkpoint invoiceCheckpoint
	err := json.Unmarshal([]byte(raw), &checkpoint)
	if err == nil && (checkpoint.Index == 0 || checkpoint.SettledAt <= 0 || checkpoint.EpochAfter < 0 || checkpoint.EpochAfter >= checkpoint.SettledAt) {
		return invoiceCheckpoint{}, fmt.Errorf("invalid invoice checkpoint; refusing to rewind")
	}
	return checkpoint, err
}

func validSettledInvoice(invoice *lnrpc.Invoice) bool {
	return invoice != nil && invoice.State == lnrpc.Invoice_SETTLED &&
		invoice.SettleIndex > 0 && invoice.AddIndex > 0 && invoice.SettleDate > 0 && len(invoice.RHash) == 32
}

func (c invoiceCheckpoint) regression(invoice *lnrpc.Invoice) bool {
	// A smaller index alone is only a replay. Require a different invoice with
	// a settlement strictly newer than the durable anchor. Timestamps have
	// second precision: ambiguous same-second regressions are not auto-reset.
	return validSettledInvoice(invoice) && c.Index > 0 && c.SettledAt > 0 &&
		invoice.SettleIndex <= c.Index && invoice.SettleDate > c.SettledAt &&
		hex.EncodeToString(invoice.RHash) != c.Hash
}

func (c invoiceCheckpoint) advance(invoice *lnrpc.Invoice, regression bool) invoiceCheckpoint {
	next := c
	if regression {
		next.EpochAfter = c.SettledAt
	}
	next.Index = invoice.SettleIndex
	next.AddIndex = invoice.AddIndex
	next.Hash = hex.EncodeToString(invoice.RHash)
	// Never let out-of-order history lower the time watermark.
	if invoice.SettleDate > next.SettledAt {
		next.SettledAt = invoice.SettleDate
	}
	return next
}

// followSettledInvoices adds one bounded ListInvoices call per connection, not
// per invoice. The ordinary monotonic stream has no extra LND calls. Recovery
// never writes to LND and only commits a checkpoint after processing succeeds.
func followSettledInvoices(ctx context.Context, client lnrpc.LightningClient, checkpoint invoiceCheckpoint,
	save func(context.Context, invoiceCheckpoint) error,
	process func(context.Context, *lnrpc.Invoice, bool) error,
	warn func(string, ...any),
) error {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	page, err := client.ListInvoices(probeCtx, &lnrpc.ListInvoiceRequest{Reversed: true, NumMaxInvoices: invoiceReconcileLimit})
	cancel()
	if err != nil {
		return fmt.Errorf("invoice reconciliation: %w", err)
	}
	if page == nil {
		return fmt.Errorf("invoice reconciliation returned no response")
	}
	if checkpoint.Index > 0 && checkpoint.SettledAt == 0 {
		// Upgrade from a scalar cursor. Only the exact indexed invoice is a
		// historical anchor; the maximum index in a mixed-generation list is not.
		for _, invoice := range page.Invoices {
			if validSettledInvoice(invoice) && invoice.SettleIndex == checkpoint.Index {
				checkpoint = checkpoint.advance(invoice, false)
			}
		}
		if checkpoint.SettledAt == 0 {
			checkpoint.SettledAt = time.Now().Unix()
			warn("invoice cursor has no anchor in the latest %d invoices; historical automatic reset deferred", invoiceReconcileLimit)
		}
		if err := save(ctx, checkpoint); err != nil {
			return err
		}
	}

	commit := func(invoice *lnrpc.Invoice, historical, regression bool) error {
		next := checkpoint.advance(invoice, regression)
		if err := process(ctx, invoice, historical); err != nil {
			return err
		}
		if err := save(ctx, next); err != nil {
			return err
		}
		if regression {
			warn("invoice settlement sequence regression recovered: %d -> %d (add_index=%d); LND database unchanged", checkpoint.Index, next.Index, next.AddIndex)
		}
		checkpoint = next
		return nil
	}

	// Do not jump over normal stream backlog using a truncated list. Reconcile
	// this page only when independent settlement evidence proves a regression.
	recovering := false
	for _, invoice := range page.Invoices {
		if checkpoint.regression(invoice) {
			recovering = true
			break
		}
	}
	if recovering {
		sort.SliceStable(page.Invoices, func(i, j int) bool {
			a, b := page.Invoices[i], page.Invoices[j]
			if a.GetSettleDate() != b.GetSettleDate() {
				return a.GetSettleDate() < b.GetSettleDate()
			}
			return a.GetSettleIndex() < b.GetSettleIndex()
		})
		fence := checkpoint.SettledAt
		for _, invoice := range page.Invoices {
			if !validSettledInvoice(invoice) || invoice.SettleDate <= fence {
				continue
			}
			regression := checkpoint.regression(invoice)
			if invoice.SettleIndex <= checkpoint.Index && !regression {
				continue
			}
			if err := commit(invoice, true, regression); err != nil {
				return err
			}
		}
	}

	stream, err := client.SubscribeInvoices(ctx, &lnrpc.InvoiceSubscription{SettleIndex: checkpoint.Index})
	if err != nil {
		return err
	}
	for {
		invoice, err := stream.Recv()
		if err != nil {
			return err
		}
		if !validSettledInvoice(invoice) || invoice.SettleDate <= checkpoint.EpochAfter {
			continue
		}
		regression := checkpoint.regression(invoice)
		if invoice.SettleIndex <= checkpoint.Index {
			if !regression {
				continue
			}
			// Independently confirm a live low-index event before replacing the
			// checkpoint. A failed/inconsistent lookup leaves it untouched.
			lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			confirmed, err := client.LookupInvoice(lookupCtx, &lnrpc.PaymentHash{RHash: invoice.RHash})
			cancel()
			if err != nil {
				return fmt.Errorf("confirm invoice regression: %w", err)
			}
			if !validSettledInvoice(confirmed) || confirmed.AddIndex != invoice.AddIndex ||
				confirmed.SettleIndex != invoice.SettleIndex || confirmed.SettleDate != invoice.SettleDate ||
				!bytes.Equal(confirmed.RHash, invoice.RHash) {
				return fmt.Errorf("inconsistent invoice regression evidence; cursor preserved")
			}
		}
		if err := commit(invoice, false, regression); err != nil {
			return err
		}
	}
}
