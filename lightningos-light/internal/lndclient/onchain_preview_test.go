package lndclient

import (
	"testing"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

func TestEstimatePreviewVirtualSizeP2WKH(t *testing.T) {
	addr, err := btcutil.NewAddressWitnessPubKeyHash(make([]byte, 20), &chaincfg.MainNetParams)
	if err != nil {
		t.Fatalf("NewAddressWitnessPubKeyHash: %v", err)
	}
	script, err := txscript.PayToAddrScript(addr)
	if err != nil {
		t.Fatalf("PayToAddrScript: %v", err)
	}

	vbytes := estimatePreviewVirtualSize(
		[]previewInput{{pkScript: script}},
		[]*wire.TxOut{wire.NewTxOut(50_000, script)},
	)
	if vbytes != 110 {
		t.Fatalf("estimatePreviewVirtualSize() = %d, want 110", vbytes)
	}
}

func TestBuildSweepAllPreview(t *testing.T) {
	addr, err := btcutil.NewAddressWitnessPubKeyHash(make([]byte, 20), &chaincfg.MainNetParams)
	if err != nil {
		t.Fatalf("NewAddressWitnessPubKeyHash: %v", err)
	}
	script, err := txscript.PayToAddrScript(addr)
	if err != nil {
		t.Fatalf("PayToAddrScript: %v", err)
	}

	preview := buildSweepAllPreview(OnchainSendPreview{
		Address:     addr.EncodeAddress(),
		SweepAll:    true,
		SatPerVbyte: 5,
	}, []OnchainUtxo{
		{AmountSat: 100_000, AddressType: "p2wkh"},
	}, script)

	if !preview.EnoughFunds {
		t.Fatalf("expected enough funds, got false with message %q", preview.Message)
	}
	if preview.FeeSat != 550 {
		t.Fatalf("fee_sat = %d, want 550", preview.FeeSat)
	}
	if preview.RecipientAmountSat != 99_450 {
		t.Fatalf("recipient_amount_sat = %d, want 99450", preview.RecipientAmountSat)
	}
	if preview.Exact {
		t.Fatalf("expected estimated preview for sweep all")
	}
}

func TestBuildSweepAllPreviewDust(t *testing.T) {
	addr, err := btcutil.NewAddressWitnessPubKeyHash(make([]byte, 20), &chaincfg.MainNetParams)
	if err != nil {
		t.Fatalf("NewAddressWitnessPubKeyHash: %v", err)
	}
	script, err := txscript.PayToAddrScript(addr)
	if err != nil {
		t.Fatalf("PayToAddrScript: %v", err)
	}

	preview := buildSweepAllPreview(OnchainSendPreview{
		Address:     addr.EncodeAddress(),
		SweepAll:    true,
		SatPerVbyte: 5,
	}, []OnchainUtxo{
		{AmountSat: 540, AddressType: "p2wkh"},
	}, script)

	if preview.EnoughFunds {
		t.Fatalf("expected insufficient funds due to dust output")
	}
	if preview.RecipientAmountSat != 0 {
		t.Fatalf("recipient_amount_sat = %d, want 0", preview.RecipientAmountSat)
	}
	if preview.Message == "" {
		t.Fatalf("expected dust warning message")
	}
}
