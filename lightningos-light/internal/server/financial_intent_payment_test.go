package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"lightningos-light/lnrpc"
	"lightningos-light/lnrpc/routerrpc"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestFinancialIntentInvoicePreservesExactNativeDefaultFee(t *testing.T) {
	client, invoice, payment := lnurlInvoiceClient(t)
	s := &Server{lnd: client}
	for _, tc := range []struct{ amountMsat, feeMsat int64 }{{1, 1}, {1001500, 50075}, {10000000001, 500000001}} {
		invoice.mu.Lock()
		invoice.decoded.NumMsat = tc.amountMsat
		invoice.decoded.NumSatoshis = tc.amountMsat / 1000
		invoice.mu.Unlock()
		normalized, binding, err := s.prepareInvoiceFinancialIntent(context.Background(), "/api/wallet/pay", []byte(`{"payment_request":"lnbc1test","max_fee_sat":0}`))
		if err != nil {
			t.Fatal(err)
		}
		var request financialInvoiceRequest
		if json.Unmarshal(normalized, &request) != nil || request.MaxFeeSat != 0 || binding.Summary.MaxFeeMsat == nil || *binding.Summary.MaxFeeMsat != tc.feeMsat {
			t.Fatal("default fee policy was rounded or changed before approval")
		}
		encoded, _ := json.Marshal(binding.Summary)
		var summary map[string]any
		if json.Unmarshal(encoded, &summary) != nil || summary["amount_msat"] != strconv.FormatInt(tc.amountMsat, 10) || summary["max_fee_msat"] != strconv.FormatInt(tc.feeMsat, 10) {
			t.Fatal("API lost exact millisatoshi precision")
		}
		// Actual gRPC request, with a simulated rejection and no funds.
		if err := client.PayInvoice(context.Background(), request.PaymentRequest, nil, request.MaxFeeSat); err == nil {
			t.Fatal("native mock did not reject payment")
		}
		if payment.lastFeeMsat.Load() != tc.feeMsat {
			t.Fatal("approved default differs from the actual native Router request")
		}
	}
}

func financialPaymentHTTPRequest(s *Server, session authSessionSnapshot, id, body, path string, handler http.Handler) (*http.Request, *httptest.ResponseRecorder) {
	r := httptest.NewRequest("POST", "https://node.example"+path, strings.NewReader(body))
	r.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: session.ID})
	r.Header.Set("Origin", "https://node.example")
	r.Header.Set("X-CSRF-Token", session.CSRFToken)
	r.Header.Set(financialIntentHeader, id)
	w := httptest.NewRecorder()
	s.auth.Middleware()(s.financialIntentMiddleware(handler)).ServeHTTP(w, r)
	return r, w
}

func TestFinancialIntentInvoiceLostRPCReplyAndNativeReconciliation(t *testing.T) {
	s, old, session := financialHTTPFixture(t)
	if _, err := s.financialIntents.cancelDraft(old.ID); err != nil {
		t.Fatal(err)
	}
	client, router := pendingRPCClient(t)
	s.lnd = client
	hash := strings.Repeat("ab", 32)
	fee := int64(2)
	binding := financialIntentBinding{Kind: "invoice_payment", Summary: financialIntentSummary{AmountSat: 100, AmountMsat: 100000, MaxFeeSat: &fee, PaymentHash: hash}}
	body := []byte(`{"payment_request":"native-fixture-invoice","amount_sat":100,"max_fee_sat":2}`)
	intent := financialHandlerApproval(t, s, session, "/api/wallet/pay", body, binding)
	router.beforeSend = func(request *routerrpc.SendPaymentRequest) error {
		stored, _, err := s.financialIntents.get(intent.ID)
		if err != nil || len(stored.Proof) == 0 || request.PaymentRequest != "native-fixture-invoice" || request.FeeLimitMsat != 2000 {
			t.Error("native payment escaped its durable approval barrier or fee ceiling")
		}
		router.payments[hash] = &lnrpc.Payment{PaymentHash: hash, Status: lnrpc.Payment_SUCCEEDED, ValueSat: 100, ValueMsat: 100000, FeeMsat: 1000, FeeSat: 1}
		return nil
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := executeFinancialInvoicePayment(r.Context(), hash, "native-fixture-invoice", nil, 2, func() error { return s.lnd.PayInvoice(r.Context(), "native-fixture-invoice", nil, 2) })
		if err != nil {
			writeError(w, 503, "native response unavailable")
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	// Use the actual authenticated middleware with the invoice route rather
	// than the on-chain convenience request fixture.
	run := func(payload []byte) int {
		r, w := financialPaymentHTTPRequest(s, session, intent.ID, string(payload), "/api/wallet/pay", handler)
		_ = r
		return w.Code
	}
	if run(intent.Request) != 503 {
		t.Fatal("lost native reply was not retained as uncertain")
	}
	stored, _, _ := s.financialIntents.get(intent.ID)
	status, result, err := s.observeFinancialIntent(context.Background(), stored)
	if err != nil || status != 200 || string(result) != `{"ok":true}` {
		t.Fatal("native invoice success not reconciled", err)
	}
	if _, err := s.financialIntents.reconcile(intent.ID, status, result); err != nil {
		t.Fatal(err)
	}
	if run(intent.Request) != 200 {
		t.Fatal("reconciled invoice result not replayed")
	}
	router.mu.Lock()
	sends := router.sends
	router.mu.Unlock()
	if sends != 1 {
		t.Fatal("invoice was dispatched more than once")
	}
}

func TestFinancialIntentChatPersistsNativeHashBeforeRPC(t *testing.T) {
	s, old, session := financialHTTPFixture(t)
	if _, err := s.financialIntents.cancelDraft(old.ID); err != nil {
		t.Fatal(err)
	}
	client, router := pendingRPCClient(t)
	s.lnd = client
	peer := "02" + strings.Repeat("11", 32)
	request, _ := json.Marshal(map[string]any{"peer_pubkey": peer, "message": "immutable message", "amount_sat": 100})
	normalized, binding, err := prepareChatFinancialIntent(request)
	if err != nil {
		t.Fatal(err)
	}
	intent := financialHandlerApproval(t, s, session, "/api/chat/send", normalized, binding)
	var nativeHash string
	router.beforeSend = func(request *routerrpc.SendPaymentRequest) error {
		nativeHash = hex.EncodeToString(request.PaymentHash)
		stored, _, _ := s.financialIntents.get(intent.ID)
		var proof financialNativeProof
		if json.Unmarshal(stored.Proof, &proof) != nil || proof.PaymentHash != nativeHash || !proof.DispatchStarted {
			t.Error("keysend hash was not durably committed before native dispatch")
		}
		if hex.EncodeToString(request.Dest) != peer || request.Amt != 100 || string(request.DestCustomRecords[34349334]) != "immutable message" || request.FeeLimitMsat != 100000 {
			t.Error("keysend changed approved destination, amount, message or fee limit")
		}
		return nil
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if validateFinancialChatExecution(r.Context(), peer, 100, "immutable message") != nil {
			t.Error("approved keysend snapshot was rejected")
		}
		if validateFinancialChatExecution(r.Context(), peer, 101, "immutable message") == nil {
			t.Error("changed keysend amount was accepted")
		}
		_, err := client.SendKeysendMessageWithPaymentHash(r.Context(), peer, 100, "immutable message", func(hash string) error { return commitFinancialChatDispatch(r.Context(), hash) })
		if err == nil {
			t.Error("native fixture did not lose reply")
		}
		writeError(w, 503, "keysend response unavailable")
	})
	_, first := financialPaymentHTTPRequest(s, session, intent.ID, string(intent.Request), "/api/chat/send", handler)
	if first.Code != 503 || len(nativeHash) != 64 {
		t.Fatal("keysend fixture failed")
	}
	stored, _, _ := s.financialIntents.get(intent.ID)
	if _, _, err := s.observeFinancialIntent(context.Background(), stored); err == nil {
		t.Fatal("missing native keysend was treated as terminal and released")
	}
	_, retry := financialPaymentHTTPRequest(s, session, intent.ID, string(intent.Request), "/api/chat/send", handler)
	if retry.Code != 409 {
		t.Fatal("unresolved keysend retry was executed")
	}
	router.mu.Lock()
	router.payments[nativeHash] = &lnrpc.Payment{PaymentHash: nativeHash, Status: lnrpc.Payment_SUCCEEDED, ValueSat: 100, ValueMsat: 100000, FeeMsat: 1000, FeeSat: 1}
	sends := router.sends
	router.mu.Unlock()
	status, result, err := s.observeFinancialIntent(context.Background(), stored)
	if err != nil || status != 200 || !strings.Contains(string(result), nativeHash) {
		t.Fatal("native keysend result not recovered", err)
	}
	if _, err := s.financialIntents.reconcile(intent.ID, status, result); err != nil {
		t.Fatal(err)
	}
	_, replay := financialPaymentHTTPRequest(s, session, intent.ID, string(intent.Request), "/api/chat/send", handler)
	if replay.Code != 200 || sends != 1 {
		t.Fatal("keysend did not preserve a single native payment")
	}
}
