package server

import (
	"context"
	"strings"
	"time"
)

// The app owns the profit and loss of its own sales, because it is the only
// place that knows a settled invoice was a channel sale rather than an ordinary
// payment. The reports module reads the revenue side from here; the on-chain
// side stays local on purpose (see MagmaPnL.OnchainCostCounted).

// MagmaPnL summarises what the sales actually earned.
type MagmaPnL struct {
	SalesCount int   `json:"sales_count"`
	RevenueSat int64 `json:"revenue_sat"`
	// OnchainCostSat is what we paid in mining fees to fund the sold channels,
	// attributed by funding txid.
	OnchainCostSat int64 `json:"onchain_cost_sat"`
	// OnchainCostResolved counts how many of those funding fees we could match.
	// A fresh open is missing from the wallet transaction list for a moment.
	OnchainCostResolved int   `json:"onchain_cost_resolved"`
	NetSat              int64 `json:"net_sat"`
	// PendingRevenueSat is money promised by orders already accepted but not yet
	// paid; it is not counted in RevenueSat.
	PendingRevenueSat int64 `json:"pending_revenue_sat"`
	PendingCount      int   `json:"pending_count"`
	// OnchainCostCounted flags that the same on-chain fee is already part of the
	// node-wide on-chain cost in the reports module. Subtracting it there as well
	// would double count it, so only the revenue side is exported.
	OnchainCostCounted bool `json:"onchain_cost_already_in_reports"`
}

// PnL builds the sales P&L over a window. A zero `since` means all time.
func (s *MagmaService) PnL(ctx context.Context, since time.Time) (MagmaPnL, error) {
	result := MagmaPnL{OnchainCostCounted: true}

	var startArg any
	if !since.IsZero() {
		startArg = since
	}
	rows, err := s.db.Query(ctx, `
select revenue_sat, coalesce(funding_txid, ''), coalesce(onchain_fee_sat, -1)
from magma_orders
where revenue_settled_at is not null
  and ($1::timestamptz is null or revenue_settled_at >= $1)
`, startArg)
	if err != nil {
		return MagmaPnL{}, err
	}
	type saleRow struct {
		revenue int64
		txid    string
		feeSat  int64
	}
	sales := make([]saleRow, 0, 16)
	for rows.Next() {
		var row saleRow
		if err := rows.Scan(&row.revenue, &row.txid, &row.feeSat); err != nil {
			rows.Close()
			return MagmaPnL{}, err
		}
		sales = append(sales, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return MagmaPnL{}, err
	}

	for _, sale := range sales {
		result.SalesCount++
		result.RevenueSat += sale.revenue
		if sale.feeSat >= 0 {
			result.OnchainCostSat += sale.feeSat
			result.OnchainCostResolved++
		}
	}
	result.NetSat = result.RevenueSat - result.OnchainCostSat

	// Accepted but unpaid: shown separately so the operator can tell a sale that
	// has landed from one that is still a promise.
	// A promise the buyer abandoned is not pending revenue, so orders Amboss has
	// closed out drop off even though our own state machine never moved them.
	if err := s.db.QueryRow(ctx, `
select coalesce(sum(revenue_sat),0), count(*)
from magma_orders
where revenue_settled_at is null and local_state = any($1)
  and not (magma_status = any($2))
`, magmaCommittedStates, magmaTerminalStatusList()).Scan(&result.PendingRevenueSat, &result.PendingCount); err != nil {
		return MagmaPnL{}, err
	}
	return result, nil
}

// magmaAliasesPerCycle bounds the gRPC calls one cycle will make. The first sync
// on an established seller has dozens of unknown buyers, and resolving them all
// at once would hammer LND for something purely cosmetic.
const magmaAliasesPerCycle = 15

// resolveBuyerAliases fills in the buyer alias from our own graph. The Amboss
// order carries only the pubkey, and a pubkey tells the operator nothing about
// who they just sold a channel to.
//
// A null column means "never attempted"; an empty string means "attempted and
// unknown", so a buyer missing from the graph is not retried on every cycle
// forever. We are peered with the buyer after the sale, so the alias normally
// arrives on the first try.
func (s *MagmaService) resolveBuyerAliases(ctx context.Context) {
	if s.lnd == nil {
		return
	}
	rows, err := s.db.Query(ctx, `
select order_id, buyer_pubkey from magma_orders
where buyer_alias is null and buyer_pubkey <> ''
order by coalesce(order_created_at, first_seen_at) desc
limit $1
`, magmaAliasesPerCycle)
	if err != nil {
		return
	}
	type target struct{ orderID, pubkey string }
	targets := make([]target, 0, magmaAliasesPerCycle)
	for rows.Next() {
		var item target
		if err := rows.Scan(&item.orderID, &item.pubkey); err == nil {
			targets = append(targets, item)
		}
	}
	rows.Close()

	// One lookup per distinct pubkey: the same buyer often has several orders.
	aliasByPubkey := make(map[string]string, len(targets))
	for _, item := range targets {
		if _, seen := aliasByPubkey[item.pubkey]; seen {
			continue
		}
		alias := ""
		if details, err := s.lnd.GetNodeDetails(ctx, item.pubkey); err == nil {
			alias = strings.TrimSpace(details.Alias)
		}
		aliasByPubkey[item.pubkey] = alias
	}
	for _, item := range targets {
		if _, err := s.db.Exec(ctx,
			`update magma_orders set buyer_alias=$2 where order_id=$1 and buyer_alias is null`,
			item.orderID, aliasByPubkey[item.pubkey]); err != nil {
			return
		}
	}
}

// resolveOnchainCosts fills in the mining fee of each sale's funding
// transaction. Run from the poller because a freshly broadcast open does not
// appear in the wallet transaction list immediately.
func (s *MagmaService) resolveOnchainCosts(ctx context.Context) {
	if s.lnd == nil {
		return
	}
	rows, err := s.db.Query(ctx, `
select order_id, funding_txid from magma_orders
where funding_txid <> '' and onchain_fee_sat is null
`)
	if err != nil {
		return
	}
	pending := make(map[string]string, 8)
	for rows.Next() {
		var orderID, txid string
		if err := rows.Scan(&orderID, &txid); err == nil {
			// Normalized because rows written before the open call's return value
			// was understood hold a full channel point here, and the wallet
			// transaction list is keyed by bare txid.
			pending[orderID] = strings.ToLower(magmaTxidFromPoint(txid))
		}
	}
	rows.Close()
	if len(pending) == 0 {
		return
	}

	provider, ok := s.lnd.(magmaOnchainTxLister)
	if !ok {
		return
	}
	transactions, err := provider.ListOnchainTransactions(ctx, 0)
	if err != nil {
		return
	}
	feeByTxid := make(map[string]int64, len(transactions))
	for _, tx := range transactions {
		feeByTxid[strings.ToLower(strings.TrimSpace(tx.Txid))] = tx.FeeSat
	}

	for orderID, txid := range pending {
		fee, found := feeByTxid[txid]
		if !found {
			continue
		}
		if _, err := s.db.Exec(ctx,
			`update magma_orders set onchain_fee_sat=$2, updated_at=now() where order_id=$1`,
			orderID, fee); err != nil && s.logger != nil {
			s.logger.Printf("magma: failed to store on-chain cost for order %s: %v", orderID, err)
		}
	}
}
