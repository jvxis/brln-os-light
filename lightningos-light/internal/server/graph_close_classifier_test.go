package server

import "testing"

func TestParseGraphCloseFundingOutpoint(t *testing.T) {
	txid, vout, err := parseGraphCloseFundingOutpoint("4157d99065512da061556906ff9a71fb40ebe5899113c3493a75751065e8a808:1")
	if err != nil {
		t.Fatalf("parseGraphCloseFundingOutpoint returned error: %v", err)
	}
	if txid != "4157d99065512da061556906ff9a71fb40ebe5899113c3493a75751065e8a808" {
		t.Fatalf("unexpected txid: %s", txid)
	}
	if vout != 1 {
		t.Fatalf("unexpected vout: %d", vout)
	}
}

func TestParseGraphCloseFundingOutpointRejectsInvalidValue(t *testing.T) {
	if _, _, err := parseGraphCloseFundingOutpoint("bad-value"); err == nil {
		t.Fatal("expected parseGraphCloseFundingOutpoint to reject invalid chan_point")
	}
}

func TestFindSpendingTransactionInBlock(t *testing.T) {
	block := bitcoinVerboseBlock{
		Tx: []bitcoinVerboseTransaction{
			{
				TxID: "close-tx",
				Vin: []bitcoinVerboseTxInput{
					{TxID: "funding-tx", Vout: 3},
				},
			},
		},
	}

	match := findSpendingTransactionInBlock(block, "funding-tx", 3)
	if match == nil {
		t.Fatal("expected to find spending transaction in block")
	}
	if match.TxID != "close-tx" {
		t.Fatalf("unexpected txid: %s", match.TxID)
	}
}

func TestGraphCloseSearchHeights(t *testing.T) {
	got := graphCloseSearchHeights(943353)
	want := []uint32{943353, 943352, 943354}
	if len(got) != len(want) {
		t.Fatalf("unexpected length: got %d want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("graphCloseSearchHeights mismatch at %d: got %d want %d", index, got[index], want[index])
		}
	}
}

func TestClassifyGraphCloseTransactionShapeMutual(t *testing.T) {
	tx := bitcoinVerboseTransaction{
		TxID: "mutual-close",
		Vout: []bitcoinVerboseTxOutput{
			{
				Value: 0.49,
				ScriptPubKey: struct {
					Type      string   `json:"type"`
					Address   string   `json:"address"`
					Addresses []string `json:"addresses"`
				}{Type: "witness_v0_keyhash"},
			},
			{
				Value: 0.50,
				ScriptPubKey: struct {
					Type      string   `json:"type"`
					Address   string   `json:"address"`
					Addresses []string `json:"addresses"`
				}{Type: "witness_v1_taproot"},
			},
		},
	}

	closeType, confidence, reason := classifyGraphCloseTransactionShape(tx)
	if closeType != "mutual_close" {
		t.Fatalf("unexpected closeType: %s", closeType)
	}
	if confidence != "high" {
		t.Fatalf("unexpected confidence: %s", confidence)
	}
	if reason != "direct_payout_outputs" {
		t.Fatalf("unexpected reason: %s", reason)
	}
}

func TestClassifyGraphCloseTransactionShapeForce(t *testing.T) {
	tx := bitcoinVerboseTransaction{
		TxID: "force-close",
		Vout: []bitcoinVerboseTxOutput{
			{
				Value: 0.87283215,
				ScriptPubKey: struct {
					Type      string   `json:"type"`
					Address   string   `json:"address"`
					Addresses []string `json:"addresses"`
				}{Type: "witness_v0_keyhash"},
			},
			{
				Value: 0.00000330,
				ScriptPubKey: struct {
					Type      string   `json:"type"`
					Address   string   `json:"address"`
					Addresses []string `json:"addresses"`
				}{Type: "witness_v0_keyhash"},
			},
			{
				Value: 0.00000330,
				ScriptPubKey: struct {
					Type      string   `json:"type"`
					Address   string   `json:"address"`
					Addresses []string `json:"addresses"`
				}{Type: "witness_v0_keyhash"},
			},
		},
	}

	closeType, confidence, reason := classifyGraphCloseTransactionShape(tx)
	if closeType != "force_close" {
		t.Fatalf("unexpected closeType: %s", closeType)
	}
	if confidence != "high" {
		t.Fatalf("unexpected confidence: %s", confidence)
	}
	if reason != "anchor_outputs_detected" {
		t.Fatalf("unexpected reason: %s", reason)
	}
}
