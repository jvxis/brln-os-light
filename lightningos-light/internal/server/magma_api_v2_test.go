package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// magmaTwoEndpointClient wires a client whose two endpoints are separate test
// servers, so which one an operation actually talks to is observable.
func magmaTwoEndpointClient(t *testing.T, market, legacy http.HandlerFunc) *magmaAmbossClient {
	t.Helper()
	m := httptest.NewServer(market)
	l := httptest.NewServer(legacy)
	t.Cleanup(m.Close)
	t.Cleanup(l.Close)
	return &magmaAmbossClient{endpoint: l.URL, marketEndpoint: m.URL, http: m.Client()}
}

func magmaJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

// Accept goes to the replacement endpoint, in the shape that schema expects:
// namespaced under market.order.seller, with the arguments in an input object
// rather than positional. The old endpoint must not be touched when it works.
func TestMagmaAcceptUsesTheMarketEndpoint(t *testing.T) {
	var mu sync.Mutex
	var gotQuery, gotInput string
	legacyHits := 0

	client := magmaTwoEndpointClient(t,
		func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Query     string         `json:"query"`
				Variables map[string]any `json:"variables"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			mu.Lock()
			gotQuery = req.Query
			raw, _ := json.Marshal(req.Variables["input"])
			gotInput = string(raw)
			mu.Unlock()
			magmaJSON(w, `{"data":{"market":{"order":{"seller":{"accept":{"success":true}}}}}}`)
		},
		func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			legacyHits++
			mu.Unlock()
			magmaJSON(w, `{"data":{"sellerAcceptOrder":true}}`)
		})

	if err := client.AcceptOrder(context.Background(), "tok", "order-1", "lnbc1..."); err != nil {
		t.Fatalf("accept should succeed: %v", err)
	}
	if !strings.Contains(gotQuery, "market") || !strings.Contains(gotQuery, "seller") {
		t.Fatalf("expected the namespaced mutation, got %q", gotQuery)
	}
	if !strings.Contains(gotInput, `"order_id":"order-1"`) ||
		!strings.Contains(gotInput, `"payment_request":"lnbc1..."`) {
		t.Fatalf("input object is wrong: %s", gotInput)
	}
	if legacyHits != 0 {
		t.Fatalf("the deprecated endpoint must not be called when the new one works, got %d call(s)", legacyHits)
	}
}

// The new endpoint has no track record here yet, and an accept that never lands
// costs the sale and a SELLER_FAILED_TO_REACT with it. One retry against the
// deprecated endpoint is cheaper than that.
func TestMagmaAcceptFallsBackToLegacy(t *testing.T) {
	legacyHits := 0
	client := magmaTwoEndpointClient(t,
		func(w http.ResponseWriter, r *http.Request) {
			magmaJSON(w, `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`)
		},
		func(w http.ResponseWriter, r *http.Request) {
			legacyHits++
			magmaJSON(w, `{"data":{"sellerAcceptOrder":true}}`)
		})

	if err := client.AcceptOrder(context.Background(), "tok", "order-2", "lnbc1..."); err != nil {
		t.Fatalf("the fallback should have carried the accept: %v", err)
	}
	if legacyHits != 1 {
		t.Fatalf("expected exactly one fallback call, got %d", legacyHits)
	}
}

// A rejected credential is the one failure the old endpoint cannot rescue, and
// retrying there would spend the same token against a second host for nothing.
func TestMagmaAcceptDoesNotFallBackOnBadCredential(t *testing.T) {
	legacyHits := 0
	client := magmaTwoEndpointClient(t,
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
		func(w http.ResponseWriter, r *http.Request) {
			legacyHits++
			magmaJSON(w, `{"data":{"sellerAcceptOrder":true}}`)
		})

	err := client.AcceptOrder(context.Background(), "tok", "order-3", "lnbc1...")
	if err == nil {
		t.Fatal("a bad token must surface, not be papered over by the fallback")
	}
	if legacyHits != 0 {
		t.Fatalf("must not retry a credential failure elsewhere, got %d call(s)", legacyHits)
	}
}

// When both refuse, the reported error is the new endpoint's. That is the one
// that matters now; the deprecated endpoint failing is expected, not news.
func TestMagmaAcceptReportsTheMarketErrorWhenBothFail(t *testing.T) {
	client := magmaTwoEndpointClient(t,
		func(w http.ResponseWriter, r *http.Request) {
			magmaJSON(w, `{"errors":[{"message":"market is down","extensions":{"code":"MARKET_CODE"}}]}`)
		},
		func(w http.ResponseWriter, r *http.Request) {
			magmaJSON(w, `{"errors":[{"message":"legacy is down","extensions":{"code":"LEGACY_CODE"}}]}`)
		})

	err := client.AcceptOrder(context.Background(), "tok", "order-4", "lnbc1...")
	if err == nil {
		t.Fatal("both endpoints refused, so this must be an error")
	}
	if !strings.Contains(err.Error(), "market is down") {
		t.Fatalf("expected the replacement endpoint's error, got %q", err)
	}
}

// success:false is a refusal, not a transport failure, and has to read as one.
func TestMagmaAcceptTreatsSuccessFalseAsRefusal(t *testing.T) {
	client := magmaTwoEndpointClient(t,
		func(w http.ResponseWriter, r *http.Request) {
			magmaJSON(w, `{"data":{"market":{"order":{"seller":{"accept":{"success":false}}}}}}`)
		},
		func(w http.ResponseWriter, r *http.Request) {
			t.Error("a clean response that refuses must not trigger the fallback")
			magmaJSON(w, `{"data":{"sellerAcceptOrder":true}}`)
		})

	if err := client.AcceptOrder(context.Background(), "tok", "order-5", "lnbc1..."); err == nil {
		t.Fatal("success:false must be reported as a refusal")
	}
}


// Reject moves too. It is what the approval deadline calls when an accept keeps
// failing, so leaving it on an endpoint Amboss says is no longer maintained
// would put the protection against SELLER_FAILED_TO_REACT on the same footing
// as the call that already broke.
func TestMagmaRejectUsesTheMarketEndpoint(t *testing.T) {
	var gotQuery, gotInput string
	legacyHits := 0
	client := magmaTwoEndpointClient(t,
		func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Query     string         `json:"query"`
				Variables map[string]any `json:"variables"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			gotQuery = req.Query
			raw, _ := json.Marshal(req.Variables["input"])
			gotInput = string(raw)
			magmaJSON(w, `{"data":{"market":{"order":{"seller":{"reject":{"success":true}}}}}}`)
		},
		func(w http.ResponseWriter, r *http.Request) {
			legacyHits++
			magmaJSON(w, `{"data":{"sellerRejectOrder":true}}`)
		})

	if err := client.RejectOrder(context.Background(), "tok", "order-r"); err != nil {
		t.Fatalf("reject should succeed: %v", err)
	}
	if !strings.Contains(gotQuery, "reject(input:") {
		t.Fatalf("expected the namespaced reject, got %q", gotQuery)
	}
	if !strings.Contains(gotInput, `"order_id":"order-r"`) {
		t.Fatalf("input object is wrong: %s", gotInput)
	}
	if legacyHits != 0 {
		t.Fatalf("must not touch the deprecated endpoint when the new one works, got %d", legacyHits)
	}
}

// The channel point keeps its shape. Amboss documents tx_id as TXID:OUTPUT_INDEX
// and states the format is unchanged from the legacy field - and submitting an
// invalid channel is the one mistake in this integration their own UI warns
// costs reputation heavily, so the format is pinned by a test rather than left
// to whoever edits this next.
func TestMagmaAddTransactionSendsTheOutpointUnchanged(t *testing.T) {
	const point = "0a5411a6a356b1531f36c7055f49d18100d227f7996f0566ecf429a6881b0c63:0"
	var gotQuery, gotInput string
	client := magmaTwoEndpointClient(t,
		func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Query     string         `json:"query"`
				Variables map[string]any `json:"variables"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			gotQuery = req.Query
			raw, _ := json.Marshal(req.Variables["input"])
			gotInput = string(raw)
			magmaJSON(w, `{"data":{"market":{"order":{"seller":{"add_transaction":{"success":true}}}}}}`)
		},
		func(w http.ResponseWriter, r *http.Request) {
			t.Error("the new endpoint answered, so there is nothing to fall back to")
			magmaJSON(w, `{"data":{"sellerAddTransaction":true}}`)
		})

	if err := client.AddTransaction(context.Background(), "tok", "order-t", point); err != nil {
		t.Fatalf("add_transaction should succeed: %v", err)
	}
	if !strings.Contains(gotQuery, "add_transaction(input:") {
		t.Fatalf("expected the namespaced mutation, got %q", gotQuery)
	}
	if !strings.Contains(gotInput, `"tx_id":"`+point+`"`) {
		t.Fatalf("the outpoint index must survive intact, got %s", gotInput)
	}
}

// A channel point without its output index never reaches either endpoint. The
// guard predates this migration and has to keep holding: the new field name
// says tx_id, which invites dropping the index.
func TestMagmaAddTransactionStillRejectsABareTxid(t *testing.T) {
	client := magmaTwoEndpointClient(t,
		func(w http.ResponseWriter, r *http.Request) { t.Error("must not reach the network") },
		func(w http.ResponseWriter, r *http.Request) { t.Error("must not reach the network") })

	err := client.AddTransaction(context.Background(), "tok", "order-t",
		"0a5411a6a356b1531f36c7055f49d18100d227f7996f0566ecf429a6881b0c63")
	if err == nil {
		t.Fatal("a txid with no output index must be refused before it is sent")
	}
}

// Both new calls inherit the fallback, so a broken replacement endpoint cannot
// strand a rejection or a channel confirmation.
func TestMagmaRejectAndAddTransactionFallBack(t *testing.T) {
	for _, tc := range []struct {
		name   string
		legacy string
		run    func(*magmaAmbossClient) error
	}{
		{"reject", `{"data":{"sellerRejectOrder":true}}`,
			func(c *magmaAmbossClient) error {
				return c.RejectOrder(context.Background(), "tok", "o")
			}},
		{"add_transaction", `{"data":{"sellerAddTransaction":true}}`,
			func(c *magmaAmbossClient) error {
				return c.AddTransaction(context.Background(), "tok", "o",
					"0a5411a6a356b1531f36c7055f49d18100d227f7996f0566ecf429a6881b0c63:0")
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits := 0
			client := magmaTwoEndpointClient(t,
				func(w http.ResponseWriter, r *http.Request) {
					magmaJSON(w, `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`)
				},
				func(w http.ResponseWriter, r *http.Request) {
					hits++
					magmaJSON(w, tc.legacy)
				})
			if err := tc.run(client); err != nil {
				t.Fatalf("the fallback should have carried it: %v", err)
			}
			if hits != 1 {
				t.Fatalf("expected one fallback call, got %d", hits)
			}
		})
	}
}
