package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"lightningos-light/lnrpc"
)

const paymentCursorRecoveryKey = "payments_cursor_recovery"
const paymentCursorProbeInterval = 5 * time.Minute

// Keep the recovery fence across restarts, independently of the advancing scalar
// cursor. Historical reconciliation must not become a new Telegram alert.
type paymentCursorRecovery struct {
	PreviousCursor string `json:"previous_cursor"`
	ResumeAfter    uint64 `json:"resume_after"`
	ReplayThrough  uint64 `json:"replay_through"`
	RecoveredAtNS  int64  `json:"recovered_at_ns"`
}

func decodePaymentCursorRecovery(raw string) (paymentCursorRecovery, error) {
	var recovery paymentCursorRecovery
	if raw == "" {
		return recovery, nil
	}
	if err := json.Unmarshal([]byte(raw), &recovery); err != nil {
		return recovery, err
	}
	previous, err := strconv.ParseUint(recovery.PreviousCursor, 10, 64)
	if err != nil || previous <= recovery.ReplayThrough || recovery.ReplayThrough <= recovery.ResumeAfter || recovery.RecoveredAtNS <= 0 {
		return paymentCursorRecovery{}, fmt.Errorf("invalid payments recovery checkpoint; cursor preserved")
	}
	return recovery, nil
}

func (r paymentCursorRecovery) suppressMirror(pay *lnrpc.Payment) bool {
	if pay == nil || r.RecoveredAtNS == 0 || pay.PaymentIndex > r.ReplayThrough {
		return false
	}
	// An old IN_FLIGHT payment that actually completes after recovery is live.
	for _, attempt := range pay.Htlcs {
		if attempt.GetResolveTimeNs() > r.RecoveredAtNS {
			return false
		}
	}
	return pay.CreationTimeNs <= r.RecoveredAtNS
}

// Query normally first. Only an empty successful response permits a bounded,
// throttled probe of the newest available indices. Empty history is ambiguous
// (e.g. deleted payments) and is never grounds to reset a cursor.
func queryPaymentPage(ctx context.Context, client lnrpc.LightningClient, offset uint64, previous string,
	recovery paymentCursorRecovery, probe bool, now time.Time,
	save func(context.Context, string, paymentCursorRecovery) error,
) (*lnrpc.ListPaymentsResponse, uint64, paymentCursorRecovery, bool, error) {
	forward := func() (*lnrpc.ListPaymentsResponse, error) {
		res, err := client.ListPayments(ctx, &lnrpc.ListPaymentsRequest{
			IncludeIncomplete: true, IndexOffset: offset, MaxPayments: paymentsPollPageSize,
		})
		if err == nil && res == nil {
			err = fmt.Errorf("payments query returned no response")
		}
		return res, err
	}
	page, err := forward()
	if err != nil || len(page.GetPayments()) != 0 || offset == 0 || !probe {
		return page, offset, recovery, false, err
	}
	tail, err := client.ListPayments(ctx, &lnrpc.ListPaymentsRequest{
		IncludeIncomplete: true, Reversed: true, MaxPayments: paymentsPollPageSize,
	})
	if err != nil {
		return page, offset, recovery, true, fmt.Errorf("payments cursor probe: %w", err)
	}
	if tail == nil {
		return page, offset, recovery, true, fmt.Errorf("payments cursor probe returned no response")
	}
	if len(tail.Payments) == 0 {
		return page, offset, recovery, true, nil
	}
	var first, last uint64
	for _, pay := range tail.Payments {
		if pay == nil || pay.PaymentIndex == 0 {
			return page, offset, recovery, true, fmt.Errorf("inconsistent payments cursor probe; cursor preserved")
		}
		if first == 0 || pay.PaymentIndex < first {
			first = pay.PaymentIndex
		}
		if pay.PaymentIndex > last {
			last = pay.PaymentIndex
		}
	}
	if last >= offset {
		return page, offset, recovery, true, nil
	}
	// A payment may have appeared while the backwards query was running.
	confirmed, err := forward()
	if err != nil || len(confirmed.GetPayments()) != 0 {
		return confirmed, offset, recovery, true, err
	}
	next := paymentCursorRecovery{
		PreviousCursor: previous, ResumeAfter: first - 1, ReplayThrough: last, RecoveredAtNS: now.UnixNano(),
	}
	if err := save(ctx, previous, next); err != nil {
		return page, offset, recovery, true, fmt.Errorf("save payments cursor recovery: %w", err)
	}
	return tail, next.ResumeAfter, next, true, nil
}

func (n *Notifier) savePaymentCursorRecovery(ctx context.Context, expected string, recovery paymentCursorRecovery) error {
	raw, err := json.Marshal(recovery)
	if err != nil {
		return err
	}
	tx, err := n.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var current string
	if err := tx.QueryRow(ctx, "select value from notification_cursors where key='payments_index' for update").Scan(&current); err != nil {
		return err
	}
	if current != expected {
		return fmt.Errorf("payments cursor changed concurrently; recovery deferred")
	}
	// An append-only backup and the active mirror fence commit with the rewind.
	_, err = tx.Exec(ctx, `insert into notification_cursors(key,value,updated_at) values ($1,$2,now())`,
		paymentCursorRecoveryKey+":"+strconv.FormatInt(recovery.RecoveredAtNS, 10), string(raw))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `insert into notification_cursors(key,value,updated_at) values ($1,$2,now())
on conflict(key) do update set value=excluded.value, updated_at=excluded.updated_at`, paymentCursorRecoveryKey, string(raw))
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "update notification_cursors set value=$1,updated_at=now() where key='payments_index'", strconv.FormatUint(recovery.ResumeAfter, 10)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (n *Notifier) pollPaymentPage(recovery *paymentCursorRecovery, nextProbe *time.Time) (*lnrpc.ListPaymentsResponse, uint64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	previous, err := n.getCursor(ctx, "payments_index")
	if err == nil {
		var raw string
		raw, err = n.getCursor(ctx, paymentCursorRecoveryKey)
		if err == nil {
			var stored paymentCursorRecovery
			stored, err = decodePaymentCursorRecovery(raw)
			if err == nil {
				*recovery = stored
			}
		}
	}
	cancel()
	if err != nil {
		return nil, 0, fmt.Errorf("read payments cursor: %w", err)
	}
	var offset uint64
	if strings.TrimSpace(previous) == "" {
		n.paymentsCatchupMode.Store(true)
	} else {
		offset, err = strconv.ParseUint(previous, 10, 64)
		if err != nil {
			return nil, 0, fmt.Errorf("invalid payments cursor; refusing historical reset")
		}
	}
	rpcCtx, rpcCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer rpcCancel()
	conn, release, err := n.lnd.BorrowLightning(rpcCtx, false)
	if err != nil {
		return nil, offset, err
	}
	defer release()
	now := time.Now()
	page, effective, updated, probed, err := queryPaymentPage(rpcCtx, lnrpc.NewLightningClient(conn), offset, previous,
		*recovery, !now.Before(*nextProbe), now, n.savePaymentCursorRecovery)
	if probed {
		*nextProbe = now.Add(paymentCursorProbeInterval)
	}
	if err == nil {
		if updated != *recovery && n.logger != nil {
			n.logger.Printf("notifications: payments cursor recovered from %s to %d; bounded replay through %d, historical Telegram alerts suppressed; previous cursor saved", previous, effective, updated.ReplayThrough)
		}
		*recovery = updated
	}
	return page, effective, err
}
