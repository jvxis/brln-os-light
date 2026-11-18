package server

import (
	"context"
	"time"

	"lightningos-light/internal/lndclient"
)

// Forward policy snapshot (0.5.58).
//
// LND's forwarding history only reports the total fee a forward earned
// (outbound fee + inbound fee, floored at zero). Once a channel carries a
// negative inbound fee, the realized price of the exits that flow leaves
// through no longer says anything about their advertised fee, and nothing in
// the stored forward tells the two apart from an ordinary fee change.
//
// The Notifier already loads every channel's current policy at the start of
// each forwards poll. This file records, per settled forward, what the
// outgoing channel's policy would have charged and what the incoming channel's
// inbound policy contributed, in a side table keyed by the forward's event
// key. The notifications table, its upsert and every reader of it are left
// untouched: a missing or failed snapshot only means the row is absent, and a
// forward without a snapshot behaves exactly as today.
//
// Nothing decides on these columns yet. They exist so that the realized sale
// price used by the rebalance retest, the exploration lots and the interlock
// floors can be cleaned of inbound discounts once a week of data exists.

const forwardPolicySnapshotLogInterval = 10 * time.Minute

type forwardPolicySnapshot struct {
	FeePolicyOutMsat    int64
	InboundFeeMsat      *int64
	InboundDiscountMsat int64
}

// computeForwardPolicySnapshot derives the snapshot from the forward's msat
// amounts and the channel policies loaded for the poll. It needs the outgoing
// channel's policy; without it there is nothing to compare the fee against.
// The inbound component is recorded only when the incoming channel is known.
func computeForwardPolicySnapshot(amtInMsat, amtOutMsat, feeMsat int64, in lndclient.ChannelInfo, inKnown bool, out lndclient.ChannelInfo, outKnown bool) (forwardPolicySnapshot, bool) {
	if !outKnown || out.FeeRatePpm == nil || amtOutMsat <= 0 {
		return forwardPolicySnapshot{}, false
	}
	snap := forwardPolicySnapshot{FeePolicyOutMsat: policyFeeMsat(derefInt64(out.BaseFeeMsat), *out.FeeRatePpm, amtOutMsat)}
	if inKnown && amtInMsat > 0 {
		inbound := policyFeeMsat(derefInt64(in.InboundBaseMsat), derefInt64(in.InboundFeeRatePpm), amtInMsat)
		snap.InboundFeeMsat = &inbound
	}
	if feeMsat < 0 {
		feeMsat = 0
	}
	if discount := snap.FeePolicyOutMsat - feeMsat; discount > 0 {
		snap.InboundDiscountMsat = discount
	}
	return snap, true
}

// policyFeeMsat mirrors LND's fee computation: base + amount × rate / 1e6,
// with the rate in parts per million of the msat amount. A negative rate
// (inbound discount) yields a negative result.
func policyFeeMsat(baseMsat, ratePpm, amountMsat int64) int64 {
	return baseMsat + (amountMsat*ratePpm)/1_000_000
}

func derefInt64(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func (n *Notifier) ensureForwardPolicySnapshotSchema(ctx context.Context) {
	if n == nil || n.db == nil {
		return
	}
	_, err := n.db.Exec(ctx, `
create table if not exists forward_policy_snapshot (
  event_key text primary key,
  occurred_at timestamptz not null,
  chan_id_in bigint,
  chan_id_out bigint,
  fee_msat bigint not null default 0,
  fee_policy_out_msat bigint not null default 0,
  inbound_fee_msat bigint,
  inbound_discount_msat bigint not null default 0,
  captured_at timestamptz not null default now()
);
create index if not exists forward_policy_snapshot_occurred_idx on forward_policy_snapshot (occurred_at desc);
create index if not exists forward_policy_snapshot_chan_out_idx on forward_policy_snapshot (chan_id_out, occurred_at desc);
`)
	if err != nil && n.logger != nil {
		n.logger.Printf("notifications: forward policy snapshot schema unavailable: %v", err)
	}
}

// storeForwardPolicySnapshot writes one row per forward and never reports
// failure to the caller: the forward itself is already stored, and a missing
// snapshot is the documented fallback. Errors are logged at most once per
// interval so a broken side table cannot flood the journal.
func (n *Notifier) storeForwardPolicySnapshot(ctx context.Context, eventKey string, occurredAt time.Time, chanIn, chanOut, feeMsat int64, snap forwardPolicySnapshot) {
	if n == nil || n.db == nil || eventKey == "" {
		return
	}
	_, err := n.db.Exec(ctx, `
insert into forward_policy_snapshot (event_key, occurred_at, chan_id_in, chan_id_out, fee_msat, fee_policy_out_msat, inbound_fee_msat, inbound_discount_msat)
values ($1, $2, $3, $4, $5, $6, $7, $8)
on conflict (event_key) do nothing
`, eventKey, occurredAt, nullableChanID(chanIn), nullableChanID(chanOut), feeMsat, snap.FeePolicyOutMsat, snap.InboundFeeMsat, snap.InboundDiscountMsat)
	if err == nil || n.logger == nil {
		return
	}
	n.forwardPolicyLogMu.Lock()
	defer n.forwardPolicyLogMu.Unlock()
	if time.Since(n.forwardPolicyLogAt) < forwardPolicySnapshotLogInterval {
		return
	}
	n.forwardPolicyLogAt = time.Now()
	n.logger.Printf("notifications: forward policy snapshot write failed: %v", err)
}

func nullableChanID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
