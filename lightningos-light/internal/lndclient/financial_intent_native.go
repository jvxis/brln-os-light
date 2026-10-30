package lndclient

import (
	"context"
	"errors"
	"fmt"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"math"
	"strings"

	"lightningos-light/lnrpc"
	"lightningos-light/lnrpc/walletrpc"
)

// FinancialIntentInput records the eligible default-account coins approved by
// the operator. It is private execution metadata, not an API response.
type FinancialIntentInput struct {
	Outpoint    string `json:"outpoint"`
	AmountSat   int64  `json:"amount_sat"`
	PkScript    string `json:"pk_script"`
	AddressType string `json:"address_type"`
}

// Includes the bounded retry already supported by cooperative CloseChannel.
func FinancialIntentCloseFeeCeiling(satPerVbyte int64) int64 {
	if satPerVbyte <= 0 || satPerVbyte > 21_000_000*100_000_000 {
		return 0
	}
	return boostedCloseMaxFeePerVbyte(deriveCloseMaxFeePerVbyte(satPerVbyte))
}

// Snapshot the exact same deterministic ceiling used by native payment
// callers, including sub-satoshi invoices. Zero keeps the existing default.
func FinancialIntentPaymentFeeLimitMsat(decoded DecodedInvoice, maxFeeSat int64) int64 {
	if maxFeeSat > 0 && maxFeeSat <= math.MaxInt64/1000 {
		return maxFeeSat * 1000
	}
	return defaultRouterPaymentFeeLimitMsatForDecodedInvoice(decoded)
}

func (c *Client) FinancialIntentNodeState(ctx context.Context) (*lnrpc.GetInfoResponse, error) {
	conn, release, err := c.borrowConn(ctx, grpcRoleAdminUnary)
	if err != nil {
		return nil, err
	}
	defer release()
	return lnrpc.NewLightningClient(conn).GetInfo(ctx, &lnrpc.GetInfoRequest{})
}

func (c *Client) RestoreFinancialIntentLabel(ctx context.Context, txid, label string) error {
	hash, err := chainhash.NewHashFromStr(txid)
	if err != nil {
		return errors.New("invalid financial transaction identifier")
	}
	conn, release, err := c.borrowConn(ctx, grpcRoleAdminUnary)
	if err != nil {
		return err
	}
	defer release()
	_, err = walletrpc.NewWalletKitClient(conn).LabelTransaction(ctx, &walletrpc.LabelTransactionRequest{Txid: hash[:], Label: label, Overwrite: true})
	return err
}

func (c *Client) FinancialIntentInputs(ctx context.Context, selection []string) ([]FinancialIntentInput, error) {
	available, err := c.previewDefaultAccountUtxos(ctx)
	if err != nil {
		return nil, err
	}
	filter, explicit, err := buildOutpointFilter(selection)
	if err != nil {
		return nil, err
	}
	result := []FinancialIntentInput{}
	for _, utxo := range available {
		point := strings.ToLower(strings.TrimSpace(utxo.Outpoint))
		if explicit {
			if _, wanted := filter[point]; !wanted {
				continue
			}
		}
		result = append(result, FinancialIntentInput{Outpoint: point, AmountSat: utxo.AmountSat, PkScript: utxo.PkScript, AddressType: utxo.AddressType})
	}
	if explicit && len(result) != len(filter) {
		return nil, errors.New("one or more approved inputs are unavailable")
	}
	return result, nil
}

// Resolve automatic fee selection before approval, including the current relay
// floor. The actual transfer continues through native SendCoins, preserving
// LND's coin-selection and anchor-reserve checks.
func (c *Client) FinancialIntentFeeRate(ctx context.Context, target int32) (int64, error) {
	if target <= 0 {
		target = 6
	}
	conn, release, err := c.borrowConn(ctx, grpcRoleAdminUnary)
	if err != nil {
		return 0, err
	}
	defer release()
	estimate, err := walletrpc.NewWalletKitClient(conn).EstimateFee(ctx, &walletrpc.EstimateFeeRequest{ConfTarget: target})
	if err != nil {
		return 0, err
	}
	kw := max(estimate.GetSatPerKw(), estimate.GetMinRelayFeeSatPerKw())
	if kw <= 0 || kw > int64(^uint64(0)>>1)-249 {
		return 0, errors.New("automatic fee estimate unavailable; choose an explicit positive fee rate")
	}
	return (kw + 249) / 250, nil
}

// The height is captured before dispatch. It bounds native reconciliation to
// recent wallet history plus unconfirmed transactions, instead of loading an
// arbitrarily long wallet history on every retry.
func (c *Client) FinancialIntentTransactions(ctx context.Context, label string, startHeight int32) ([]*lnrpc.Transaction, error) {
	if !strings.HasPrefix(label, "los-intent:") || len(label) != len("los-intent:")+32 {
		return nil, errors.New("invalid managed transaction label")
	}
	if startHeight < 0 {
		startHeight = 0
	}
	conn, release, err := c.borrowConn(ctx, grpcRoleAdminUnary)
	if err != nil {
		return nil, err
	}
	defer release()
	response, err := lnrpc.NewLightningClient(conn).GetTransactions(ctx, &lnrpc.GetTransactionsRequest{StartHeight: startHeight, EndHeight: -1, Account: "default"})
	if err != nil {
		return nil, err
	}
	matches := []*lnrpc.Transaction{}
	for _, transaction := range response.GetTransactions() {
		if transaction.GetLabel() == label {
			matches = append(matches, transaction)
		}
	}
	return matches, nil
}

type FinancialIntentChannel struct {
	Memo         string
	ChannelPoint string
	Pubkey       string
	CapacitySat  int64
	Private      bool
}

// Return the sweeper's existing budget, or the pinned native wallet-CPFP
// default (half the UTXO value). Reading unconfirmed coins here does not change
// the confirmed-input eligibility used for ordinary wallet sends.
func (c *Client) FinancialIntentBumpBudget(ctx context.Context, outpoint string) (int64, error) {
	sweeps, err := c.ListPendingSweeps(ctx)
	if err != nil {
		return 0, err
	}
	for _, sweep := range sweeps {
		if sweep.Outpoint == outpoint && sweep.BudgetSat > 0 {
			return sweep.BudgetSat, nil
		}
	}
	conn, release, err := c.borrowConn(ctx, grpcRoleAdminUnary)
	if err != nil {
		return 0, err
	}
	defer release()
	response, err := walletrpc.NewWalletKitClient(conn).ListUnspent(ctx, &walletrpc.ListUnspentRequest{MinConfs: 0, MaxConfs: math.MaxInt32})
	if err != nil {
		return 0, err
	}
	for _, utxo := range response.GetUtxos() {
		point := utxo.GetOutpoint()
		if point == nil {
			continue
		}
		txid := point.GetTxidStr()
		if txid == "" {
			txid = txidFromBytes(point.GetTxidBytes())
		}
		if fmt.Sprintf("%s:%d", txid, point.GetOutputIndex()) == outpoint && utxo.GetAmountSat() > 0 {
			return (utxo.GetAmountSat() + 1) / 2, nil
		}
	}
	return 0, errors.New("native fee budget unavailable for this input")
}

func (c *Client) FinancialIntentChannels(ctx context.Context, memo string) ([]FinancialIntentChannel, error) {
	if !strings.HasPrefix(memo, "los-intent:") || len(memo) != len("los-intent:")+32 {
		return nil, errors.New("invalid managed channel memo")
	}
	conn, release, err := c.borrowConn(ctx, grpcRoleAdminUnary)
	if err != nil {
		return nil, err
	}
	defer release()
	client := lnrpc.NewLightningClient(conn)
	channels, err := client.ListChannels(ctx, &lnrpc.ListChannelsRequest{})
	if err != nil {
		return nil, err
	}
	pending, err := client.PendingChannels(ctx, &lnrpc.PendingChannelsRequest{})
	if err != nil {
		return nil, err
	}
	result := []FinancialIntentChannel{}
	seen := map[string]bool{}
	add := func(item FinancialIntentChannel) {
		if (item.Memo == memo || strings.HasPrefix(item.Memo, memo+":")) && !seen[item.ChannelPoint] {
			seen[item.ChannelPoint] = true
			result = append(result, item)
		}
	}
	for _, ch := range channels.GetChannels() {
		add(FinancialIntentChannel{Memo: ch.GetMemo(), ChannelPoint: ch.GetChannelPoint(), Pubkey: ch.GetRemotePubkey(), CapacitySat: ch.GetCapacity(), Private: ch.GetPrivate()})
	}
	addPending := func(ch *lnrpc.PendingChannelsResponse_PendingChannel) {
		if ch != nil {
			add(FinancialIntentChannel{Memo: ch.GetMemo(), ChannelPoint: ch.GetChannelPoint(), Pubkey: ch.GetRemoteNodePub(), CapacitySat: ch.GetCapacity(), Private: ch.GetPrivate()})
		}
	}
	for _, ch := range pending.GetPendingOpenChannels() {
		addPending(ch.GetChannel())
	}
	for _, ch := range pending.GetPendingClosingChannels() {
		addPending(ch.GetChannel())
	}
	for _, ch := range pending.GetPendingForceClosingChannels() {
		addPending(ch.GetChannel())
	}
	for _, ch := range pending.GetWaitingCloseChannels() {
		addPending(ch.GetChannel())
	}
	return result, nil
}
