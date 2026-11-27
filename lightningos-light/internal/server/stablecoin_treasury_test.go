package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTreasuryPublicInputAndCaps(t *testing.T) {
	if mainnetTreasuryInvoice("lnbcrt123") || mainnetTreasuryInvoice("lntb123") ||
		!mainnetTreasuryInvoice("lnbc1example") || !mainnetTreasuryInvoice("lnbc123n1example") {
		t.Fatal("network prefix must reject regtest and testnet invoices")
	}
	if got := treasuryRecipient(" Receiver@PAY.Example.com "); got != "Receiver@pay.example.com" {
		t.Fatalf("normalized Lightning Address = %q", got)
	}
	for _, bad := range []string{"https://pay.example.com", "bad@pay.example.com/path", strings.Repeat("x", 266) + "@example.com"} {
		if treasuryRecipient(bad) != "" {
			t.Fatalf("accepted invalid Lightning Address %q", bad)
		}
	}
	if treasurySaturateFee(1700) != 17 || treasurySaturateFee(treasuryMaxSats) != 200 {
		t.Fatal("unexpected Lightning fee cap")
	}
	if validTreasuryWallet(strings.Repeat("1", 32)) || !validTreasuryWallet("At4mSNudfMM87kRVbsWr2VYriLqAxJfz2xN19WpgnZ7m") {
		t.Fatal("incorrect Solana public address validation")
	}
	if treasuryProviderFee(&treasuryBridgeFee{Amount: 21045, Asset: "USDC", Decimals: 0}) != nil ||
		treasuryProviderFee(&treasuryBridgeFee{Amount: 21045, Asset: "USDC", Decimals: 6}).Atoms != 21045 {
		t.Fatal("incorrect bridge fee units")
	}
}

func TestTreasuryOrderReconciliationDisclosure(t *testing.T) {
	order := treasuryOrder{Quote: treasuryQuote{Receive: treasuryAmount{Atoms: 1663, Asset: "sats"}}, State: "delivery_pending",
		StatusToken: "private-bridge-token", RefundHash: "private-funding-record"}
	if (treasuryWorkerResult{AmountOut: 1646, DeliveryRef: "receipt"}).Arrived(order) ||
		!(treasuryWorkerResult{AmountOut: 1647, DeliveryRef: "receipt"}).Arrived(order) ||
		(treasuryWorkerResult{AmountOut: 1663}).Arrived(order) {
		t.Fatal("settlement did not require reference and quoted tolerance")
	}
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, order.view())
	for _, private := range []string{order.StatusToken, order.RefundHash} {
		if strings.Contains(rec.Body.String(), private) {
			t.Fatal("private order field exposed")
		}
	}
}

func TestTreasuryRequiresProtectedSession(t *testing.T) {
	if !authRequiresSession(treasurySignerPath) {
		t.Fatal("Treasury signer page bypasses authenticated Manager routes")
	}
	s := &Server{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/apps/stablecoin-treasury/status", nil)
	s.handleTreasuryStatus(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status without protected session = %d", rec.Code)
	}
}
