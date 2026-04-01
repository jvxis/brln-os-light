package lndclient

import "testing"

func TestBuildCreateInvoiceRequestDefaults(t *testing.T) {
	req := buildCreateInvoiceRequest(20_000, "memo", 0, nil)
	if req == nil {
		t.Fatal("expected request")
	}
	if req.Value != 20_000 {
		t.Fatalf("expected value 20000, got %d", req.Value)
	}
	if req.Memo != "memo" {
		t.Fatalf("expected memo to be preserved, got %q", req.Memo)
	}
	if req.Expiry != 3600 {
		t.Fatalf("expected default expiry 3600, got %d", req.Expiry)
	}
	if req.IsBlinded {
		t.Fatal("expected non-blinded invoice by default")
	}
	if req.BlindedPathConfig != nil {
		t.Fatal("expected no blinded path config by default")
	}
}

func TestBuildCreateInvoiceRequestBlindedWithIncomingChannel(t *testing.T) {
	req := buildCreateInvoiceRequest(20_000, "memo", 120, &CreateInvoiceOptions{
		IsBlinded:          true,
		IncomingChannelIDs: []uint64{907322492874129409},
	})
	if req == nil {
		t.Fatal("expected request")
	}
	if !req.IsBlinded {
		t.Fatal("expected blinded invoice")
	}
	if req.BlindedPathConfig == nil {
		t.Fatal("expected blinded path config")
	}
	if len(req.BlindedPathConfig.IncomingChannelList) != 1 {
		t.Fatalf("expected 1 incoming channel, got %d", len(req.BlindedPathConfig.IncomingChannelList))
	}
	if got := req.BlindedPathConfig.IncomingChannelList[0]; got != 907322492874129409 {
		t.Fatalf("unexpected incoming channel id %d", got)
	}
	if req.Expiry != 120 {
		t.Fatalf("expected expiry 120, got %d", req.Expiry)
	}
}

func TestBuildCreateInvoiceRequestIncomingChannelEnablesBlinding(t *testing.T) {
	req := buildCreateInvoiceRequest(20_000, "", 3600, &CreateInvoiceOptions{
		IncomingChannelIDs: []uint64{123},
	})
	if req == nil {
		t.Fatal("expected request")
	}
	if !req.IsBlinded {
		t.Fatal("expected incoming channel selection to enable blinding")
	}
	if req.BlindedPathConfig == nil {
		t.Fatal("expected blinded path config")
	}
}
