package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStrikeBTCAmounts(t *testing.T) {
	for _, test := range []struct {
		sat  int64
		text string
	}{
		{0, "0.00000000"},
		{1, "0.00000001"},
		{50_000, "0.00050000"},
		{2_500_000, "0.02500000"},
		{100_000_000, "1.00000000"},
	} {
		if got := satsToBTC(test.sat); got != test.text {
			t.Fatalf("satsToBTC(%d)=%q, want %q", test.sat, got, test.text)
		}
		got, err := strikeBTCToSats(test.text)
		if err != nil || got != test.sat {
			t.Fatalf("strikeBTCToSats(%q)=(%d,%v), want %d", test.text, got, err, test.sat)
		}
	}
	if _, err := strikeBTCToSats("0.000000009"); err == nil {
		t.Fatal("expected a non-zero sub-satoshi amount to be rejected")
	}
}

func TestStrikeClientFreeOnchainFlow(t *testing.T) {
	const (
		apiKey  = "strike-test-key"
		address = "bc1qtestaddress"
		quoteID = "11111111-1111-4111-8111-111111111111"
		payment = "22222222-2222-4222-8222-222222222222"
	)
	idempotencyKey := "33333333-3333-4333-8333-333333333333"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+apiKey {
			t.Fatalf("Authorization=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/balances":
			_, _ = w.Write([]byte(`[{"currency":"BTC","available":"0.02500000"}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/payment-quotes/onchain/tiers":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["btcAddress"] != address {
				t.Fatalf("btcAddress=%v", body["btcAddress"])
			}
			_, _ = w.Write([]byte(`[{"id":"tier_free","estimatedDeliveryDurationInMin":720,"estimatedFee":{"amount":"0","currency":"BTC"}}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/payment-quotes/onchain":
			if got := r.Header.Get("idempotency-key"); got != idempotencyKey {
				t.Fatalf("idempotency-key=%q", got)
			}
			var body struct {
				SourceCurrency string `json:"sourceCurrency"`
				OnchainTierID  string `json:"onchainTierId"`
				Beneficiary    struct {
					IsOwnDestination bool   `json:"isOwnDestination"`
					DestinationType  string `json:"destinationType"`
				} `json:"beneficiary"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.SourceCurrency != "BTC" || body.OnchainTierID != "tier_free" ||
				!body.Beneficiary.IsOwnDestination || body.Beneficiary.DestinationType != "SELF_CUSTODY_WALLET" {
				t.Fatalf("unexpected quote body: %+v", body)
			}
			_, _ = w.Write([]byte(`{"paymentQuoteId":"` + quoteID + `","totalFee":{"amount":"0","currency":"BTC"},"totalAmount":{"amount":"0.02500000","currency":"BTC"},"estimatedDeliveryDurationInMin":720}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/payment-quotes/"+quoteID+"/execute":
			_, _ = w.Write([]byte(`{"paymentId":"` + payment + `","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/payments/"+payment:
			_, _ = w.Write([]byte(`{"paymentId":"` + payment + `","state":"COMPLETED","onchain":{"txnId":"txid123"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newLoopOutBRLNStrikeClient(apiKey)
	client.baseURL = server.URL
	client.client = server.Client()
	ctx := context.Background()

	balances, err := client.balances(ctx)
	if err != nil || len(balances) != 1 || balances[0].Available != "0.02500000" {
		t.Fatalf("balances=(%+v,%v)", balances, err)
	}
	tiers, err := client.tiers(ctx, address, 2_500_000)
	if err != nil || len(tiers) != 1 || tiers[0].ID != "tier_free" {
		t.Fatalf("tiers=(%+v,%v)", tiers, err)
	}
	quote, err := client.createQuote(ctx, address, 2_500_000, tiers[0].ID, idempotencyKey)
	if err != nil || quote.PaymentQuoteID != quoteID {
		t.Fatalf("quote=(%+v,%v)", quote, err)
	}
	executed, err := client.executeQuote(ctx, quote.PaymentQuoteID)
	if err != nil || executed.PaymentID != payment || executed.State != "PENDING" {
		t.Fatalf("execute=(%+v,%v)", executed, err)
	}
	completed, err := client.payment(ctx, payment)
	if err != nil || completed.Onchain == nil || completed.Onchain.TxID != "txid123" {
		t.Fatalf("payment=(%+v,%v)", completed, err)
	}
}

func TestStrikeClientDecodesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"data":{"code":"PAYMENT_PROCESSED","message":"already processed","status":422,"values":{"paymentId":"payment-123"}}}`))
	}))
	defer server.Close()

	client := newLoopOutBRLNStrikeClient("key")
	client.baseURL = server.URL
	client.client = server.Client()
	_, err := client.executeQuote(context.Background(), "quote-123")
	var apiErr *loopOutBRLNStrikeAPIError
	if !errors.As(err, &apiErr) || apiErr.Code != "PAYMENT_PROCESSED" || apiErr.Values["paymentId"] != "payment-123" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestNormalizeLoopOutBRLNStrikeReturnRequiresStrikeAddress(t *testing.T) {
	base := LoopOutBRLNRequest{
		LightningAddress: "name@example.com",
		TotalSat:         100_000,
		TrancheSat:       50_000,
		TimeoutSeconds:   120,
		MaxFeePPM:        2_500,
		MinLocalPercent:  60,
	}
	base.StrikeReturnEnabled = true
	if _, err := normalizeLoopOutBRLNRequest(base); err == nil {
		t.Fatal("expected non-Strike destination to be rejected")
	}
	base.LightningAddress = "jvx@strike.me"
	if _, err := normalizeLoopOutBRLNRequest(base); err != nil {
		t.Fatalf("Strike destination rejected: %v", err)
	}
}
