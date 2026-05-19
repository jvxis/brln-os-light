package server

import "testing"

func TestParseBitcoinCoreBool(t *testing.T) {
	raw := `
server=1
txindex=1
# txindex=0
`
	if !parseBitcoinCoreBool(raw, "txindex") {
		t.Fatal("expected txindex=true")
	}
}

func TestParseBitcoinCoreBoolFalseWhenMissing(t *testing.T) {
	if parseBitcoinCoreBool("server=1\n", "txindex") {
		t.Fatal("expected txindex=false")
	}
}

func TestParseBitcoinCoreTxIndexInfoSynced(t *testing.T) {
	ready, known, err := parseBitcoinCoreTxIndexInfo(`{"txindex":{"synced":true,"best_block_height":892044}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !known || !ready {
		t.Fatalf("expected known and ready, got known=%v ready=%v", known, ready)
	}
}

func TestParseBitcoinCoreTxIndexInfoRPCEnvelopeSynced(t *testing.T) {
	ready, known, err := parseBitcoinCoreTxIndexInfo(`{"result":{"txindex":{"synced":true,"best_block_height":892044}}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !known || !ready {
		t.Fatalf("expected known and ready, got known=%v ready=%v", known, ready)
	}
}

func TestParseBitcoinCoreTxIndexInfoMissing(t *testing.T) {
	ready, known, err := parseBitcoinCoreTxIndexInfo(`{}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if known || ready {
		t.Fatalf("expected missing txindex, got known=%v ready=%v", known, ready)
	}
}
