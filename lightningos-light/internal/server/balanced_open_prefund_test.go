package server

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"lightningos-light/lnrpc"

	"github.com/btcsuite/btcd/wire"
)

func TestBalancedTransitWalletHistoryRejectsAmbiguityAndKnownSpends(t *testing.T) {
	script := append([]byte{0, 20}, bytes.Repeat([]byte{1}, 20)...)
	transit := balancedOpenTransitDetails{OutputSat: 50095, OutputScript: hex.EncodeToString(script)}
	label := "balanced-open-fixture-initiator"
	makeRecord := func(tx *wire.MsgTx, recordLabel string) *lnrpc.Transaction {
		t.Helper()
		var raw bytes.Buffer
		if err := tx.Serialize(&raw); err != nil {
			t.Fatal(err)
		}
		return &lnrpc.Transaction{TxHash: tx.TxHash().String(), RawTxHex: hex.EncodeToString(raw.Bytes()), Label: recordLabel}
	}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: 1}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(transit.OutputSat, script))
	record := makeRecord(tx, label)
	got, err := balancedTransitFromWalletHistory(transit, []*lnrpc.Transaction{record, record}, label)
	if err != nil || got.TxID != tx.TxHash().String() || got.Vout != 0 {
		t.Fatalf("unique wallet output was not recovered: %v", err)
	}
	other := tx.Copy()
	other.TxIn[0].PreviousOutPoint.Index = 2
	spend := wire.NewMsgTx(2)
	point := wire.OutPoint{Hash: tx.TxHash(), Index: 0}
	spend.AddTxIn(wire.NewTxIn(&point, nil, nil))
	spend.AddTxOut(wire.NewTxOut(49000, script))
	for name, records := range map[string][]*lnrpc.Transaction{
		"none":          nil,
		"wrong-label":   {makeRecord(tx, "unrelated")},
		"ambiguous":     {record, makeRecord(other, label)},
		"known-spent":   {record, makeRecord(spend, "recovery")},
		"missing-raw":   {{TxHash: record.TxHash, Label: label}},
		"malformed":     {{RawTxHex: "xyz", Label: label}},
		"hash-mismatch": {{TxHash: strings.Repeat("11", 32), RawTxHex: record.RawTxHex, Label: label}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := balancedTransitFromWalletHistory(transit, records, label); err == nil {
				t.Fatal("uncertain history was accepted")
			}
		})
	}
}
