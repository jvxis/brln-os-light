package lndclient

import (
	"bytes"
	"context"
	"errors"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"lightningos-light/lnrpc/walletrpc"
	"time"
)

type MeshFundedTransaction struct {
	PSBT    []byte
	Leases  []*walletrpc.UtxoLease
	Preview OnchainSendPreview
}

// FundMeshTransaction reserves confirmed inputs and returns a stable, unsigned
// proposal. FinalizeMeshTransaction only signs it; neither method broadcasts.
func (c *Client) FundMeshTransaction(ctx context.Context, address string, amount, rate int64) (*MeshFundedTransaction, error) {
	if amount <= 0 || amount > 21_000_000*100_000_000 || rate < 1 || rate > 1000 {
		return nil, errors.New("invalid amount or fee rate")
	}
	if _, err := previewAddressScript(address); err != nil {
		return nil, errors.New("Bitcoin mainnet address required")
	}
	if err := c.MeshMainnetReady(ctx); err != nil {
		return nil, err
	}
	conn, release, err := c.borrowConn(ctx, grpcRoleAdminUnary)
	if err != nil {
		return nil, err
	}
	defer release()
	client := walletrpc.NewWalletKitClient(conn)
	resp, err := client.FundPsbt(ctx, &walletrpc.FundPsbtRequest{Template: &walletrpc.FundPsbtRequest_Raw{Raw: &walletrpc.TxTemplate{Outputs: map[string]uint64{address: uint64(amount)}}}, Fees: &walletrpc.FundPsbtRequest_SatPerVbyte{SatPerVbyte: uint64(rate)}, MinConfs: 1})
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			releasePreviewLeases(cleanup, client, resp.LockedUtxos)
		}
	}()
	packet, err := psbt.NewFromRawBytes(bytes.NewReader(resp.FundedPsbt), false)
	if err != nil || packet.UnsignedTx == nil {
		return nil, errors.New("invalid funded transaction")
	}
	p := OnchainSendPreview{Address: address, RequestedAmountSat: amount, RecipientAmountSat: amount, SatPerVbyte: rate, Exact: true, EnoughFunds: true, SelectedInputCount: len(packet.UnsignedTx.TxIn)}
	var total int64
	for _, lease := range resp.LockedUtxos {
		p.SelectedInputSat += int64(lease.Value)
	}
	for _, out := range packet.UnsignedTx.TxOut {
		total += out.Value
	}
	p.FeeSat = p.SelectedInputSat - total
	p.TotalDebitSat = amount + p.FeeSat
	p.ChangeSat = txOutputValueAt(packet.UnsignedTx.TxOut, int(resp.ChangeOutputIndex))
	if p.FeeSat < 0 || p.SelectedInputCount == 0 || packet.UnsignedTx.SerializeSize() > 12000 {
		return nil, errors.New("transaction exceeds LOS Mesh limits")
	}
	keep = true
	return &MeshFundedTransaction{PSBT: resp.FundedPsbt, Leases: resp.LockedUtxos, Preview: p}, nil
}
func (c *Client) FinalizeMeshTransaction(ctx context.Context, funded *MeshFundedTransaction) ([]byte, error) {
	conn, release, err := c.borrowConn(ctx, grpcRoleAdminUnary)
	if err != nil {
		return nil, err
	}
	defer release()
	resp, err := walletrpc.NewWalletKitClient(conn).FinalizePsbt(ctx, &walletrpc.FinalizePsbtRequest{FundedPsbt: funded.PSBT})
	if err != nil {
		return nil, err
	}
	return resp.RawFinalTx, nil
}
func (c *Client) ReleaseMeshTransaction(ctx context.Context, funded *MeshFundedTransaction) {
	if funded == nil {
		return
	}
	conn, release, err := c.borrowConn(ctx, grpcRoleAdminUnary)
	if err != nil {
		return
	}
	defer release()
	releasePreviewLeases(ctx, walletrpc.NewWalletKitClient(conn), funded.Leases)
}
