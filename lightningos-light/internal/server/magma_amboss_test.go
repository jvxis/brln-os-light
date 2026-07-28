package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// magmaFixtureOrders mirrors real Amboss payloads captured from a live seller
// account. Every awkward shape here was observed in production data: numbers
// arriving as JSON strings on some fields and JSON numbers on others, nulls on
// fields that only apply mid-flight, an always-empty timeout, and channel points
// with a non-zero vout.
const magmaFixtureOrders = `{
  "getUser": {
    "market": {
      "offer_orders": {
        "list": [
          {
            "id": "99682ca1-84c0-4174-8227-2555ca37177d",
            "status": "CHANNEL_MONITORING_FINISHED",
            "account": "02db0e54f6692c1dfaf2298a7900d4cb315835572f07f34c157090c5c9c91e3f2a",
            "offer": "06358ae2-cedc-4d4d-9496-ebfb8ad284d6",
            "offer_side": "SELL",
            "size": "1000000",
            "seller_invoice_amount": "4068",
            "buyer_invoice_amount": "4068",
            "amboss_fee_rate": "0",
            "fixed_fee": "568",
            "variable_fee": "3500",
            "locked_fee_rate": 3500,
            "locked_base_fee": 568,
            "locked_fee_rate_cap": 900,
            "locked_base_fee_cap": 0,
            "locked_min_block_length": 8640,
            "blocks_until_can_be_closed": null,
            "closed_blocks_before_min": 353,
            "fee_above_cap_seconds": "21267",
            "payment_status": "SUCCESSFUL_PAYMENT",
            "payment_hash": "0451d588cfa41563",
            "channel_id": "820617x1293x0",
            "transaction_id": "e673ac3007bd55c1b78954455c6be33102542515bc1d89d138562acad496ce37:1",
            "created_at": "2024-10-28T11:05:49.076Z",
            "updated_at": "2026-01-16T17:40:45.563Z",
            "is_automated": true,
            "chat_enabled": false,
            "cancellation_reason": null,
            "seller_close_side": "PEER",
            "buyer_close_side": null,
            "timeout": ""
          },
          {
            "id": "e835d8e9-cd9b-43ee-9405-dc8da1662395",
            "status": "WAITING_FOR_SELLER_APPROVAL",
            "account": "024a5329f9663a5bca7893a176d585877b2ed9969052f5c1444b1d2ad82239e49f",
            "offer": "3e3d6898-0b5c-4e57-92fe-ff9fa96d7798",
            "offer_side": "SELL",
            "size": "5000000",
            "seller_invoice_amount": "61816",
            "buyer_invoice_amount": "64316",
            "amboss_fee_rate": "500",
            "fixed_fee": "6816",
            "variable_fee": "55000",
            "locked_fee_rate": 11000,
            "locked_base_fee": 6816,
            "locked_fee_rate_cap": 500,
            "locked_base_fee_cap": 1,
            "locked_min_block_length": 25920,
            "blocks_until_can_be_closed": 25920,
            "closed_blocks_before_min": null,
            "fee_above_cap_seconds": null,
            "payment_status": null,
            "payment_hash": null,
            "channel_id": null,
            "transaction_id": null,
            "created_at": "2024-01-13T15:22:09.091Z",
            "updated_at": "2024-01-13T15:22:09.091Z",
            "is_automated": false,
            "chat_enabled": true,
            "cancellation_reason": null,
            "seller_close_side": null,
            "buyer_close_side": null,
            "timeout": ""
          }
        ]
      }
    }
  }
}`

func newMagmaTestClient(t *testing.T, handler http.HandlerFunc) *magmaAmbossClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &magmaAmbossClient{endpoint: server.URL, http: server.Client()}
}

func magmaFixtureHandler(payload string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":` + payload + `}`))
	}
}

func TestMagmaSellerOrdersDecodesRealPayload(t *testing.T) {
	client := newMagmaTestClient(t, magmaFixtureHandler(magmaFixtureOrders))
	orders, err := client.SellerOrders(context.Background(), "token")
	if err != nil {
		t.Fatalf("SellerOrders: %v", err)
	}
	if len(orders) != 2 {
		t.Fatalf("expected 2 orders, got %d", len(orders))
	}

	finished := orders[0]
	if finished.SizeSat != 1_000_000 {
		t.Errorf("size from JSON string: got %d, want 1000000", finished.SizeSat)
	}
	if finished.RevenueSat != 4068 {
		t.Errorf("revenue from JSON string: got %d, want 4068", finished.RevenueSat)
	}
	if finished.PricePPM != 3500 {
		t.Errorf("price ppm from JSON number: got %d, want 3500", finished.PricePPM)
	}
	if finished.FeeRateCapPPM != 900 || finished.BaseFeeCapSat != 0 {
		t.Errorf("caps: got rate=%d base=%d, want 900/0", finished.FeeRateCapPPM, finished.BaseFeeCapSat)
	}
	if finished.CommitmentBlocks != 8640 {
		t.Errorf("commitment blocks: got %d, want 8640", finished.CommitmentBlocks)
	}
	if finished.FeeAboveCapSeconds == nil || *finished.FeeAboveCapSeconds != 21267 {
		t.Errorf("fee_above_cap_seconds arrives as a string and must survive: %v", finished.FeeAboveCapSeconds)
	}
	if finished.BlocksUntilCanBeClosed != nil {
		t.Errorf("null blocks_until_can_be_closed must stay nil, got %v", *finished.BlocksUntilCanBeClosed)
	}
	if finished.ClosedBlocksBeforeMin == nil || *finished.ClosedBlocksBeforeMin != 353 {
		t.Errorf("closed_blocks_before_min: %v", finished.ClosedBlocksBeforeMin)
	}
	if finished.CreatedAt == nil || finished.CreatedAt.Year() != 2024 {
		t.Errorf("created_at not parsed: %v", finished.CreatedAt)
	}

	pending := orders[1]
	if pending.PaymentStatus != "" || pending.ChannelPoint != "" {
		t.Errorf("null strings must decode empty, got payment=%q channel_point=%q",
			pending.PaymentStatus, pending.ChannelPoint)
	}
	if pending.BlocksUntilCanBeClosed == nil || *pending.BlocksUntilCanBeClosed != 25920 {
		t.Errorf("live order must carry its commitment countdown: %v", pending.BlocksUntilCanBeClosed)
	}
}

// The channel point must round-trip with its vout. Real orders carry both :0 and
// :1, so assuming :0 would confirm the wrong outpoint to Amboss on a share of
// every seller's sales.
func TestMagmaOrderKeepsChannelPointVout(t *testing.T) {
	client := newMagmaTestClient(t, magmaFixtureHandler(magmaFixtureOrders))
	orders, err := client.SellerOrders(context.Background(), "token")
	if err != nil {
		t.Fatalf("SellerOrders: %v", err)
	}
	want := "e673ac3007bd55c1b78954455c6be33102542515bc1d89d138562acad496ce37:1"
	if orders[0].ChannelPoint != want {
		t.Fatalf("channel point: got %q, want %q", orders[0].ChannelPoint, want)
	}
}

// seller_invoice_amount is already the seller's net: it equals fixed + variable,
// and the Amboss cut is added on top for the buyer. Treating it as gross and
// deducting the fees would make a policy engine reject perfectly good orders.
func TestMagmaOrderRevenueIsNetOfAmbossFee(t *testing.T) {
	client := newMagmaTestClient(t, magmaFixtureHandler(magmaFixtureOrders))
	orders, err := client.SellerOrders(context.Background(), "token")
	if err != nil {
		t.Fatalf("SellerOrders: %v", err)
	}
	for _, order := range orders {
		if got := order.PriceFixedSat + order.PriceVariableSat; got != order.RevenueSat {
			t.Errorf("order %s: fixed+variable=%d, revenue=%d", order.ID, got, order.RevenueSat)
		}
		if got := order.SizeSat * order.PricePPM / 1_000_000; got != order.PriceVariableSat {
			t.Errorf("order %s: size*ppm=%d, variable=%d", order.ID, got, order.PriceVariableSat)
		}
		want := order.RevenueSat + order.SizeSat*order.AmbossFeePPM/1_000_000
		if order.BuyerPaysSat != want {
			t.Errorf("order %s: buyer pays %d, want %d", order.ID, order.BuyerPaysSat, want)
		}
	}
}

func TestMagmaPricePerDayPPM(t *testing.T) {
	// Same headline price, very different deals: 180 days locks the capital for
	// six months at the price a 7-day order pays.
	short := MagmaOrder{PricePPM: 3500, CommitmentBlocks: 1008}
	long := MagmaOrder{PricePPM: 3500, CommitmentBlocks: 25920}
	if short.PricePerDayPPM() <= long.PricePerDayPPM() {
		t.Fatalf("short commitment must price higher per day: short=%.2f long=%.2f",
			short.PricePerDayPPM(), long.PricePerDayPPM())
	}
	if zero := (MagmaOrder{PricePPM: 3500}).PricePerDayPPM(); zero != 0 {
		t.Fatalf("unknown commitment must not divide by zero, got %.2f", zero)
	}
}

// Amboss reports business failures as HTTP 200 with a populated errors[] array.
// A status-code-only check reads those as success.
func TestMagmaErrorsInHTTP200AreFailures(t *testing.T) {
	client := newMagmaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"order not found"}]}`))
	})
	_, err := client.SellerOrders(context.Background(), "token")
	if err == nil {
		t.Fatal("expected an error for HTTP 200 with errors[]")
	}
	var apiErr *magmaAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected magmaAPIError, got %T: %v", err, err)
	}
	if apiErr.Error() != "order not found" {
		t.Errorf("message: got %q", apiErr.Error())
	}
}

func TestMagmaUnauthorizedIsDistinct(t *testing.T) {
	t.Run("http 401", func(t *testing.T) {
		client := newMagmaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})
		if _, err := client.MarketSummary(context.Background(), "token"); !errors.Is(err, errMagmaUnauthorized) {
			t.Fatalf("expected errMagmaUnauthorized, got %v", err)
		}
	})
	// An expired token can also come back as 200 with the complaint in errors[].
	t.Run("expiry reported in errors", func(t *testing.T) {
		client := newMagmaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"errors":[{"message":"Token has expired"}]}`))
		})
		if _, err := client.MarketSummary(context.Background(), "token"); !errors.Is(err, errMagmaUnauthorized) {
			t.Fatalf("expected errMagmaUnauthorized, got %v", err)
		}
	})
}

func TestMagmaClientSendsBearerToken(t *testing.T) {
	client := newMagmaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Errorf("Authorization header: got %q", got)
		}
		_, _ = w.Write([]byte(`{"data":{"getUser":{"market":{"enabled":true,"has_active_offers":false,"pending_seller_orders":2,"pending_buyer_orders":0}}}}`))
	})
	summary, err := client.MarketSummary(context.Background(), "secret-token")
	if err != nil {
		t.Fatalf("MarketSummary: %v", err)
	}
	if !summary.Enabled || summary.PendingSellerOrders != 2 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestMagmaClientRejectsEmptyToken(t *testing.T) {
	client := newMagmaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("no request should be sent without a token")
	})
	if _, err := client.MarketSummary(context.Background(), "   "); err == nil {
		t.Fatal("expected an error for an unset token")
	}
}

func TestMagmaNumberDecoding(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		wantValue int64
		wantValid bool
		wantErr   bool
	}{
		{name: "json string", raw: `"1000000"`, wantValue: 1_000_000, wantValid: true},
		{name: "json number", raw: `8640`, wantValue: 8640, wantValid: true},
		{name: "null", raw: `null`},
		{name: "empty string", raw: `""`},
		{name: "float number", raw: `2.0`, wantValue: 2, wantValid: true},
		{name: "zero stays valid", raw: `0`, wantValid: true},
		{name: "garbage string", raw: `"abc"`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var number magmaNumber
			err := json.Unmarshal([]byte(tc.raw), &number)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected a decode error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if number.Value != tc.wantValue || number.Valid != tc.wantValid {
				t.Fatalf("got {%d %t}, want {%d %t}", number.Value, number.Valid, tc.wantValue, tc.wantValid)
			}
		})
	}
}

// The Amboss credential is a JWT that expires. Without this, a working install
// goes stale silently, and the dangerous case is expiry between opening the
// channel and confirming it to Magma.
func TestMagmaTokenExpiry(t *testing.T) {
	// Real test-token payload: {"version":0.1,"iat":1785273092,"exp":1787865092,
	// "iss":"amboss.tech","sub":"..."}
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
		"eyJ2ZXJzaW9uIjowLjEsImlhdCI6MTc4NTI3MzA5MiwiZXhwIjoxNzg3ODY1MDkyLCJpc3MiOiJhbWJvc3MudGVjaCJ9." +
		"signature"
	expiry, ok := magmaTokenExpiry(token)
	if !ok {
		t.Fatal("expected the JWT expiry to decode")
	}
	if want := time.Unix(1787865092, 0).UTC(); !expiry.Equal(want) {
		t.Fatalf("expiry: got %s, want %s", expiry, want)
	}

	// An opaque API key has no known expiry; that is not an error.
	if _, ok := magmaTokenExpiry("plain-api-key"); ok {
		t.Fatal("a non-JWT token must not report an expiry")
	}
	if _, ok := magmaTokenExpiry(""); ok {
		t.Fatal("an empty token must not report an expiry")
	}
}

func TestMagmaShouldNotifyKeepsAlertsNarrow(t *testing.T) {
	cases := []struct {
		name           string
		order          MagmaOrder
		isNew          bool
		previousStatus string
		want           bool
	}{
		{
			name:  "seller must approve",
			order: MagmaOrder{Status: "WAITING_FOR_SELLER_APPROVAL"},
			isNew: true,
			want:  true,
		},
		{
			name:  "seller must open the channel",
			order: MagmaOrder{Status: "WAITING_FOR_CHANNEL_OPEN"},
			isNew: true,
			want:  true,
		},
		{
			name:  "recorded failure against us",
			order: MagmaOrder{Status: "SELLER_FAILED_TO_REACT"},
			isNew: false,
			want:  true,
		},
		{
			// First sync imports years of history; alerting on all of it would
			// bury the one order that actually needs attention.
			name:  "historical order on first sync stays quiet",
			order: MagmaOrder{Status: "CHANNEL_MONITORING_FINISHED"},
			isNew: true,
			want:  false,
		},
		{
			name:           "buyer walked away",
			order:          MagmaOrder{Status: "BUYER_REJECTED"},
			previousStatus: "WAITING_FOR_BUYER_PAYMENT",
			want:           true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := magmaShouldNotify(tc.order, tc.isNew, tc.previousStatus); got != tc.want {
				t.Fatalf("got %t, want %t", got, tc.want)
			}
		})
	}
}
