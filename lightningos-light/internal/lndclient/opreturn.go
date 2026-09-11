package lndclient

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"lightningos-light/lnrpc"
	"lightningos-light/lnrpc/walletrpc"
)

// OPReturnScript applies the application's conservative policy, independently
// of the chain backend's relay policy. AddData emits a minimal data push.
func OPReturnScript(text string) ([]byte, error) {
	if !utf8.ValidString(text) || len(text) == 0 || len(text) > 80 {
		return nil, errors.New("message must contain 1 to 80 valid UTF-8 bytes")
	}
	for _, r := range text {
		if unicode.IsControl(r) || !unicode.IsPrint(r) {
			return nil, errors.New("message must contain printable text only")
		}
	}
	return txscript.NewScriptBuilder().AddOp(txscript.OP_RETURN).AddData([]byte(text)).Script()
}

type OPReturnQuote struct {
	Text               string `json:"text"`
	PayloadHex         string `json:"payload_hex"`
	ByteCount          int    `json:"byte_count"`
	SelectedInputCount int    `json:"selected_input_count"`
	SelectedInputSat   int64  `json:"selected_input_sat"`
	EstimatedVbytes    int64  `json:"estimated_vbytes"`
	SatPerVbyte        int64  `json:"sat_per_vbyte"`
	FeeSat             int64  `json:"fee_sat"`
	TotalDebitSat      int64  `json:"total_debit_sat"`
}

// OPReturnFunding owns the WalletKit connection and leases until Release.
// It is never serialized or passed outside the in-process manager.
type OPReturnFunding struct {
	Quote  OPReturnQuote
	kit    walletrpc.WalletKitClient
	packet *psbt.Packet
	funded []byte
	leases []*walletrpc.UtxoLease
	close  func() error
}

func (f *OPReturnFunding) Release() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var result error
	for _, lease := range f.leases {
		if lease == nil || lease.Outpoint == nil || len(lease.Id) == 0 {
			result = errors.Join(result, errors.New("invalid WalletKit lease"))
			continue
		}
		_, err := f.kit.ReleaseOutput(ctx, &walletrpc.ReleaseOutputRequest{Id: lease.Id, Outpoint: lease.Outpoint})
		result = errors.Join(result, err)
	}
	return errors.Join(result, f.close())
}

func (c *Client) OPReturnReady(ctx context.Context) error {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return err
	}
	defer conn.Close()
	info, err := lnrpc.NewLightningClient(conn).GetInfo(ctx, &lnrpc.GetInfoRequest{})
	if err != nil {
		return err
	}
	if !info.SyncedToChain || len(info.Chains) != 1 || info.Chains[0] == nil || info.Chains[0].Chain != "bitcoin" || info.Chains[0].Network != "mainnet" {
		return errors.New("a synchronized, unlocked Bitcoin mainnet LND wallet is required")
	}
	return nil
}

func (c *Client) FundOPReturn(ctx context.Context, text string, rate int64) (*OPReturnFunding, error) {
	if err := c.OPReturnReady(ctx); err != nil {
		return nil, err
	}
	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	f, err := fundOPReturn(ctx, walletrpc.NewWalletKitClient(conn), text, rate)
	if err != nil {
		conn.Close()
		return nil, err
	}
	f.close = conn.Close
	return f, nil
}

func fundOPReturn(ctx context.Context, kit walletrpc.WalletKitClient, text string, rate int64) (*OPReturnFunding, error) {
	script, err := OPReturnScript(text)
	if err != nil {
		return nil, err
	}
	if rate < 1 || rate > 1000 {
		return nil, errors.New("sat_per_vbyte must be between 1 and 1000")
	}
	tx := wire.NewMsgTx(2)
	tx.AddTxOut(wire.NewTxOut(0, script))
	p, err := psbt.NewFromUnsignedTx(tx)
	if err != nil {
		return nil, err
	}
	var template bytes.Buffer
	if err := p.Serialize(&template); err != nil {
		return nil, err
	}
	resp, err := kit.FundPsbt(ctx, &walletrpc.FundPsbtRequest{
		Template: &walletrpc.FundPsbtRequest_Psbt{Psbt: template.Bytes()},
		Fees:     &walletrpc.FundPsbtRequest_SatPerVbyte{SatPerVbyte: uint64(rate)}, MinConfs: 1,
	})
	if err != nil {
		return nil, err
	}
	f := &OPReturnFunding{kit: kit, funded: resp.FundedPsbt, leases: resp.LockedUtxos, close: func() error { return nil }}
	f.packet, err = psbt.NewFromRawBytes(bytes.NewReader(resp.FundedPsbt), false)
	if err == nil {
		f.Quote, err = inspectOPReturn(f.packet, int(resp.ChangeOutputIndex), script, text, rate)
	}
	if err != nil {
		return nil, errors.Join(err, f.Release())
	}
	return f, nil
}

func inspectOPReturn(p *psbt.Packet, change int, script []byte, text string, rate int64) (OPReturnQuote, error) {
	q := OPReturnQuote{Text: text, PayloadHex: hex.EncodeToString([]byte(text)), ByteCount: len(text), SatPerVbyte: rate}
	if p == nil || p.UnsignedTx == nil || len(p.Inputs) == 0 || len(p.Inputs) != len(p.UnsignedTx.TxIn) {
		return q, errors.New("invalid funded PSBT")
	}
	outputs := p.UnsignedTx.TxOut
	// Only the requested nulldata output and WalletKit's wallet change may exist.
	if len(outputs) != 2 || change < 0 || change >= len(outputs) {
		return q, errors.New("expected OP_RETURN and wallet change only")
	}
	for i, out := range outputs {
		if i == change {
			if out.Value <= 0 || txscript.IsUnspendable(out.PkScript) {
				return q, errors.New("invalid wallet change")
			}
		} else if out.Value != 0 || !bytes.Equal(out.PkScript, script) {
			return q, errors.New("unexpected funded output")
		}
	}
	inputs := make([]previewInput, 0, len(p.Inputs))
	seen := map[wire.OutPoint]bool{}
	for i, input := range p.Inputs {
		point := p.UnsignedTx.TxIn[i].PreviousOutPoint
		if seen[point] {
			return q, errors.New("duplicate input")
		}
		seen[point] = true
		utxo := input.WitnessUtxo
		if input.NonWitnessUtxo != nil {
			if input.NonWitnessUtxo.TxHash() != point.Hash || int(point.Index) >= len(input.NonWitnessUtxo.TxOut) {
				return q, errors.New("invalid previous transaction")
			}
			previous := input.NonWitnessUtxo.TxOut[point.Index]
			if utxo != nil && (utxo.Value != previous.Value || !bytes.Equal(utxo.PkScript, previous.PkScript)) {
				return q, errors.New("inconsistent input")
			}
			utxo = previous
		}
		if utxo == nil || utxo.Value <= 0 || utxo.Value > 21_000_000*100_000_000 {
			return q, errors.New("missing input value")
		}
		q.SelectedInputSat += utxo.Value
		if q.SelectedInputSat > 21_000_000*100_000_000 {
			return q, errors.New("input amount overflow")
		}
		inputs = append(inputs, previewInput{pkScript: utxo.PkScript})
	}
	q.SelectedInputCount = len(inputs)
	q.EstimatedVbytes = estimatePreviewVirtualSize(inputs, outputs)
	q.FeeSat = q.SelectedInputSat - outputs[change].Value
	q.TotalDebitSat = q.FeeSat
	if q.FeeSat <= 0 || q.EstimatedVbytes <= 0 {
		return q, errors.New("invalid funded fee")
	}
	return q, nil
}

func (f *OPReturnFunding) Finalize(ctx context.Context) (string, string, error) {
	resp, err := f.kit.FinalizePsbt(ctx, &walletrpc.FinalizePsbtRequest{FundedPsbt: f.funded})
	if err != nil {
		return "", "", err
	}
	var tx wire.MsgTx
	if err := tx.Deserialize(bytes.NewReader(resp.RawFinalTx)); err != nil {
		return "", "", err
	}
	// Strip signatures and witness and compare the entire transaction skeleton.
	unsigned := tx.Copy()
	for _, in := range unsigned.TxIn {
		in.SignatureScript = nil
		in.Witness = nil
	}
	var got, want bytes.Buffer
	_ = unsigned.Serialize(&got)
	_ = f.packet.UnsignedTx.Serialize(&want)
	if !bytes.Equal(got.Bytes(), want.Bytes()) {
		return "", "", errors.New("finalized transaction differs from approved PSBT")
	}
	return tx.TxHash().String(), hex.EncodeToString(resp.RawFinalTx), nil
}

func (c *Client) OPReturnTransaction(ctx context.Context, txid string) (*lnrpc.Transaction, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return walletrpc.NewWalletKitClient(conn).GetTransaction(ctx, &walletrpc.GetTransactionRequest{Txid: txid})
}
