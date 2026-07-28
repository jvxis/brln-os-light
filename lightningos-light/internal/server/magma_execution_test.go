package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// magmaTestJWT builds a token that expires at the given instant, so the
// credential gate can be exercised without a live Amboss key.
func magmaTestJWT(t *testing.T, expiry time.Time) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"iss": "amboss.tech",
		"iat": expiry.Add(-30 * 24 * time.Hour).Unix(),
		"exp": expiry.Unix(),
	})
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

// The worst outcome available in this app is a token dying between OpenChannel
// and sellerAddTransaction: the funds are committed but the sale cannot be
// confirmed. Funding therefore demands a comfortable margin, while read-only
// calls only need the token to still be valid.
func TestMagmaTokenUsableGuardsChannelFunding(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name                string
		token               string
		requireSafetyWindow bool
		wantErr             bool
		wantErrContains     string
	}{
		{
			name:    "empty token",
			token:   "   ",
			wantErr: true, wantErrContains: "not configured",
		},
		{
			name:    "expired token blocks even read-only calls",
			token:   magmaTestJWT(t, now.Add(-time.Minute)),
			wantErr: true, wantErrContains: "expired",
		},
		{
			name:                "expiring in 2h blocks funding",
			token:               magmaTestJWT(t, now.Add(2*time.Hour)),
			requireSafetyWindow: true,
			wantErr:             true, wantErrContains: "before opening a channel",
		},
		{
			// Still fine for reads: accepting an order commits no funds, and the
			// invoice can simply go unpaid.
			name:                "expiring in 2h still allows read-only work",
			token:               magmaTestJWT(t, now.Add(2*time.Hour)),
			requireSafetyWindow: false,
		},
		{
			name:                "comfortable margin allows funding",
			token:               magmaTestJWT(t, now.Add(72*time.Hour)),
			requireSafetyWindow: true,
		},
		{
			// An opaque API key carries no readable expiry; refusing it would
			// break a perfectly valid credential.
			name:                "opaque non-JWT token is allowed",
			token:               "plain-api-key",
			requireSafetyWindow: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := magmaTokenUsable(tc.token, now, tc.requireSafetyWindow)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				if !strings.Contains(err.Error(), tc.wantErrContains) {
					t.Fatalf("error %q does not mention %q", err, tc.wantErrContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// Confirming a bare txid or a malformed outpoint after the channel is already
// funded is the expensive mistake this guard exists to prevent.
func TestMagmaLooksLikeChannelPoint(t *testing.T) {
	valid := "e673ac3007bd55c1b78954455c6be33102542515bc1d89d138562acad496ce37"
	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "vout 0", value: valid + ":0", want: true},
		{name: "vout 1", value: valid + ":1", want: true},
		{name: "bare txid without vout", value: valid},
		{name: "trailing colon only", value: valid + ":"},
		{name: "short txid", value: "abc:0"},
		{name: "non hex txid", value: strings.Repeat("z", 64) + ":0"},
		{name: "negative vout", value: valid + ":-1"},
		{name: "empty", value: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := magmaLooksLikeChannelPoint(tc.value); got != tc.want {
				t.Fatalf("got %t, want %t for %q", got, tc.want, tc.value)
			}
		})
	}
}

// These mutations answer with a bare scalar. A false there is a genuine failure
// even though the HTTP status and the errors[] array both look clean.
func TestMagmaMutationSucceeded(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  bool
	}{
		{name: "true", value: true, want: true},
		{name: "false", value: false},
		{name: "nil", value: nil},
		{name: "empty string", value: ""},
		{name: "literal false string", value: "false"},
		{name: "id string", value: "99682ca1-84c0", want: true},
		{name: "zero number", value: float64(0)},
		{name: "non-zero number", value: float64(1), want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := magmaMutationSucceeded(tc.value); got != tc.want {
				t.Fatalf("got %t, want %t", got, tc.want)
			}
		})
	}
}

// The mutation argument is called "request" and takes the bolt11 payment
// request. Sending the payment hash instead would be accepted by the type system
// and rejected by the buyer's wallet.
func TestMagmaAcceptOrderSendsBolt11(t *testing.T) {
	var captured map[string]any
	client := newMagmaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		captured = body.Variables
		_, _ = w.Write([]byte(`{"data":{"sellerAcceptOrder":true}}`))
	})
	const bolt11 = "lnbc40680n1pexample"
	if err := client.AcceptOrder(context.Background(), "token", "order-1", bolt11); err != nil {
		t.Fatalf("AcceptOrder: %v", err)
	}
	if captured["sellerAcceptOrderId"] != "order-1" {
		t.Errorf("order id: got %v", captured["sellerAcceptOrderId"])
	}
	if captured["request"] != bolt11 {
		t.Errorf("request must carry the bolt11, got %v", captured["request"])
	}
}

func TestMagmaAcceptOrderFailsOnFalseScalar(t *testing.T) {
	client := newMagmaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"sellerAcceptOrder":false}}`))
	})
	if err := client.AcceptOrder(context.Background(), "token", "order-1", "lnbc1"); err == nil {
		t.Fatal("a false scalar must be treated as a failure")
	}
}

func TestMagmaAddTransactionSendsFullOutpoint(t *testing.T) {
	var captured map[string]any
	client := newMagmaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		captured = body.Variables
		_, _ = w.Write([]byte(`{"data":{"sellerAddTransaction":true}}`))
	})
	point := "e673ac3007bd55c1b78954455c6be33102542515bc1d89d138562acad496ce37:1"
	if err := client.AddTransaction(context.Background(), "token", "order-1", point); err != nil {
		t.Fatalf("AddTransaction: %v", err)
	}
	if captured["transaction"] != point {
		t.Fatalf("transaction must keep the vout, got %v", captured["transaction"])
	}
}

// The request must never leave the process if the outpoint is malformed: by this
// stage the channel is already funded.
func TestMagmaAddTransactionRejectsBareTxid(t *testing.T) {
	client := newMagmaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("no request should be sent for a malformed channel point")
	})
	txid := "e673ac3007bd55c1b78954455c6be33102542515bc1d89d138562acad496ce37"
	if err := client.AddTransaction(context.Background(), "token", "order-1", txid); err == nil {
		t.Fatal("expected a validation error for a bare txid")
	}
}

func TestMagmaRejectOrderSendsOrderID(t *testing.T) {
	var captured map[string]any
	client := newMagmaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		captured = body.Variables
		_, _ = w.Write([]byte(`{"data":{"sellerRejectOrder":true}}`))
	})
	if err := client.RejectOrder(context.Background(), "token", "order-9"); err != nil {
		t.Fatalf("RejectOrder: %v", err)
	}
	if captured["sellerRejectOrderId"] != "order-9" {
		t.Fatalf("order id: got %v", captured["sellerRejectOrderId"])
	}
}

func TestMagmaNodeAddressesFallback(t *testing.T) {
	client := newMagmaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"getNode":{"graph_info":{"node":{"addresses":[
      {"addr":"1.2.3.4:9735"},{"addr":""},{"addr":"abc.onion:9735"}]}}}}}`))
	})
	addresses, err := client.NodeAddresses(context.Background(), "token", "02aa")
	if err != nil {
		t.Fatalf("NodeAddresses: %v", err)
	}
	if len(addresses) != 2 || addresses[0] != "1.2.3.4:9735" || addresses[1] != "abc.onion:9735" {
		t.Fatalf("unexpected addresses: %#v", addresses)
	}
}
