package lndclient

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"google.golang.org/grpc"
	"lightningos-light/lnrpc"
	"lightningos-light/lnrpc/walletrpc"
)

func TestOPReturnUTF8Policy(t *testing.T) {
	for _, tc := range []struct {
		text  string
		bytes int
		valid bool
	}{
		{"hello", 5, true}, {"ação", 6, true}, {"😀", 4, true}, {strings.Repeat("😀", 20), 80, true},
		{strings.Repeat("a", 80), 80, true}, {strings.Repeat("é", 40), 80, true},
		{"", 0, false}, {string([]byte{0xff}), 1, false}, {"a\x00b", 3, false}, {"a\nb", 3, false},
		{"\t", 1, false}, {"\u0085", 2, false}, {"\u200b", 3, false}, {strings.Repeat("😀", 21), 84, false},
		{strings.Repeat("a", 81), 81, false}, {"1", 1, true},
	} {
		script, err := OPReturnScript(tc.text)
		if (err == nil) != tc.valid {
			t.Fatalf("policy bytes=%d valid=%v err=%v", tc.bytes, tc.valid, err)
		}
		if !tc.valid {
			continue
		}
		if len(tc.text) != tc.bytes || script[0] != txscript.OP_RETURN || !txscript.IsNullData(script) {
			t.Fatalf("invalid canonical script %x", script)
		}
		tok := txscript.MakeScriptTokenizer(0, script)
		tok.Next()
		if !tok.Next() || tok.Next() {
			t.Fatal("expected a single canonical push")
		}
		if tc.bytes > 75 && script[1] != txscript.OP_PUSHDATA1 {
			t.Fatal("missing canonical PUSHDATA1")
		}
	}
}

type opreturnKitStub struct {
	walletrpc.WalletKitClient
	mutate      func(*psbt.Packet)
	cancel      context.CancelFunc
	released    int
	finalizeErr bool
	drift       bool
	packet      *psbt.Packet
}

func (k *opreturnKitStub) FundPsbt(ctx context.Context, r *walletrpc.FundPsbtRequest, _ ...grpc.CallOption) (*walletrpc.FundPsbtResponse, error) {
	p, err := psbt.NewFromRawBytes(bytes.NewReader(r.GetPsbt()), false)
	if err != nil {
		return nil, err
	}
	if r.MinConfs != 1 || r.SpendUnconfirmed || len(p.UnsignedTx.TxIn) != 0 || len(p.UnsignedTx.TxOut) != 1 || p.UnsignedTx.TxOut[0].Value != 0 {
		return nil, errors.New("unsafe funding template")
	}
	p.UnsignedTx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: 1}, nil, nil))
	script := append([]byte{0, 20}, make([]byte, 20)...)
	p.Inputs = []psbt.PInput{{WitnessUtxo: wire.NewTxOut(10000, script)}}
	p.UnsignedTx.AddTxOut(wire.NewTxOut(9800, script))
	p.Outputs = append(p.Outputs, psbt.POutput{})
	if k.mutate != nil {
		k.mutate(p)
	}
	k.packet = p
	var out bytes.Buffer
	if err = p.Serialize(&out); err != nil {
		return nil, err
	}
	if k.cancel != nil {
		k.cancel()
	}
	return &walletrpc.FundPsbtResponse{FundedPsbt: out.Bytes(), ChangeOutputIndex: 1, LockedUtxos: []*walletrpc.UtxoLease{{Id: make([]byte, 32), Outpoint: &lnrpc.OutPoint{OutputIndex: 1}}, {Id: make([]byte, 32), Outpoint: &lnrpc.OutPoint{OutputIndex: 2}}}}, nil
}
func (k *opreturnKitStub) ReleaseOutput(ctx context.Context, _ *walletrpc.ReleaseOutputRequest, _ ...grpc.CallOption) (*walletrpc.ReleaseOutputResponse, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	k.released++
	return &walletrpc.ReleaseOutputResponse{}, nil
}
func (k *opreturnKitStub) FinalizePsbt(ctx context.Context, _ *walletrpc.FinalizePsbtRequest, _ ...grpc.CallOption) (*walletrpc.FinalizePsbtResponse, error) {
	if k.finalizeErr {
		return nil, errors.New("finalization failed")
	}
	tx := k.packet.UnsignedTx.Copy()
	tx.TxIn[0].Witness = wire.TxWitness{[]byte{1}}
	if k.drift {
		tx.TxOut[1].Value--
	}
	var raw bytes.Buffer
	_ = tx.Serialize(&raw)
	return &walletrpc.FinalizePsbtResponse{RawFinalTx: raw.Bytes()}, nil
}

func TestOPReturnFundingInspectionAndLeaseCleanup(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*psbt.Packet)
		valid  bool
	}{
		{"valid", nil, true},
		{"nonzero nulldata", func(p *psbt.Packet) { p.UnsignedTx.TxOut[0].Value = 1 }, false},
		{"changed payload", func(p *psbt.Packet) { p.UnsignedTx.TxOut[0].PkScript = []byte{txscript.OP_RETURN} }, false},
		{"extra output", func(p *psbt.Packet) {
			p.UnsignedTx.AddTxOut(wire.NewTxOut(0, []byte{txscript.OP_RETURN}))
			p.Outputs = append(p.Outputs, psbt.POutput{})
		}, false},
		{"missing input amount", func(p *psbt.Packet) { p.Inputs[0].WitnessUtxo = nil }, false},
		{"negative fee", func(p *psbt.Packet) { p.UnsignedTx.TxOut[1].Value = 11000 }, false},
		{"unspendable change", func(p *psbt.Packet) { p.UnsignedTx.TxOut[1].PkScript = []byte{txscript.OP_RETURN} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			kit := &opreturnKitStub{mutate: tc.mutate, cancel: cancel}
			f, err := fundOPReturn(ctx, kit, "ação", 1)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if f != nil {
				if f.Quote.FeeSat != 200 || f.Quote.TotalDebitSat != 200 || f.Quote.ByteCount != 6 {
					t.Fatal(f.Quote)
				}
				if err = f.Release(); err != nil {
					t.Fatal(err)
				}
			}
			if kit.released != 2 {
				t.Fatalf("released %d leases, want 2", kit.released)
			}
		})
	}
}

func TestOPReturnFinalizeAndSkeletonGuard(t *testing.T) {
	for _, tc := range []struct {
		name        string
		fail, drift bool
	}{{"valid", false, false}, {"RPC error", true, false}, {"changed transaction", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			kit := &opreturnKitStub{finalizeErr: tc.fail, drift: tc.drift}
			f, err := fundOPReturn(context.Background(), kit, "test", 1)
			if err != nil {
				t.Fatal(err)
			}
			id, raw, err := f.Finalize(context.Background())
			if tc.fail || tc.drift {
				if err == nil {
					t.Fatal("expected rejection")
				}
			} else if err != nil || len(id) != 64 || raw == "" {
				t.Fatal("missing deterministic transaction")
			}
			if err = f.Release(); err != nil || kit.released != 2 {
				t.Fatal("leases not released")
			}
		})
	}
}
