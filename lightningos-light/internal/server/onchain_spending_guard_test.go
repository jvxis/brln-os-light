package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"
)

func onchainGuardFixture(t *testing.T) *financialIntentStore {
	t.Helper()
	store, err := openFinancialIntentStore(financialPrivateTestDir(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func onchainGuardBinding(t *testing.T, amount int64) (string, financialIntentBinding) {
	t.Helper()
	id, err := newFinancialIntentID()
	if err != nil {
		t.Fatal(err)
	}
	fee := int64(99999)
	return id, financialIntentBinding{Kind: "onchain_send", NativeLabel: "los-intent:" + id, Summary: financialIntentSummary{AmountSat: amount, SelectedInputSat: amount + 100000, MaxFeeSat: &fee}}
}

func TestOnchainGuardFixedValueExcludesFeeAndChange(t *testing.T) {
	s := onchainGuardFixture(t)
	if err := s.updateOnchainGuard(onchainGuardSettings{Enabled: true, MaxSendSat: 1000, Rolling24hLimitSat: 1000}, false); err != nil {
		t.Fatal(err)
	}
	id, binding := onchainGuardBinding(t, 1000)
	if active, err := s.reserveOnchainGuard(id, binding, ""); err != nil || !active {
		t.Fatalf("fee/input value incorrectly counted: %v", err)
	}
	if err := s.finishOnchainGuard(id, "tx-id", 1000, false); err != nil {
		t.Fatal(err)
	}
	status, err := s.onchainGuardStatus()
	if err != nil || status.UsedSat != 1000 || status.ReservedSat != 0 || status.RemainingSat != 0 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if err := s.finishOnchainGuard(id, "replacement-rbf-tx-id", 1000, false); err != nil {
		t.Fatal(err)
	}
	if status, _ := s.onchainGuardStatus(); status.UsedSat != 1000 {
		t.Fatal("replay/RBF counted twice")
	}
}

func TestOnchainGuardConcurrentReservationsAndRestart(t *testing.T) {
	s := onchainGuardFixture(t)
	if err := s.updateOnchainGuard(onchainGuardSettings{Enabled: true, Rolling24hLimitSat: 500}, false); err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int64
	var workers sync.WaitGroup
	for i := 0; i < 24; i++ {
		id, binding := onchainGuardBinding(t, 100)
		workers.Add(1)
		go func() {
			defer workers.Done()
			if _, err := s.reserveOnchainGuard(id, binding, ""); err == nil {
				accepted.Add(1)
			}
		}()
	}
	workers.Wait()
	if accepted.Load() != 5 {
		t.Fatalf("concurrent limit bypassed: accepted=%d", accepted.Load())
	}
	dir := s.dir
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openFinancialIntentStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	status, err := reopened.onchainGuardStatus()
	if err != nil || !status.Enabled || status.ReservedSat != 500 {
		t.Fatalf("restart lost policy/holds: %+v %v", status, err)
	}
	id, binding := onchainGuardBinding(t, 1)
	if _, err := reopened.reserveOnchainGuard(id, binding, ""); err == nil {
		t.Fatal("restart reset allowance")
	}
}

func TestOnchainGuardUncertainNeverExpiresAndRollingWindow(t *testing.T) {
	s := onchainGuardFixture(t)
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	if err := s.updateOnchainGuard(onchainGuardSettings{Enabled: true, Rolling24hLimitSat: 1000}, false); err != nil {
		t.Fatal(err)
	}
	id, binding := onchainGuardBinding(t, 1000)
	if _, err := s.reserveOnchainGuard(id, binding, ""); err != nil {
		t.Fatal(err)
	}
	now = now.Add(48 * time.Hour)
	if status, _ := s.onchainGuardStatus(); status.ReservedSat != 1000 {
		t.Fatal("ambiguous send aged out of budget")
	}
	if err := s.finishOnchainGuard(id, "tx", 1000, false); err != nil {
		t.Fatal(err)
	}
	now = now.Add(24*time.Hour + time.Second)
	if status, _ := s.onchainGuardStatus(); status.UsedSat != 0 || status.RemainingSat != 1000 {
		t.Fatal("settled rolling window did not expire")
	}
}

func TestOnchainGuardRelaxationRequiresFreshApproval(t *testing.T) {
	s := onchainGuardFixture(t)
	if err := s.updateOnchainGuard(onchainGuardSettings{Enabled: true, MaxSendSat: 1000, Rolling24hLimitSat: 5000}, false); err != nil {
		t.Fatal(err)
	}
	for _, next := range []onchainGuardSettings{
		{Enabled: false}, {Enabled: true, MaxSendSat: 2000, Rolling24hLimitSat: 5000}, {Enabled: true, MaxSendSat: 1000},
	} {
		if err := s.updateOnchainGuard(next, false); !errors.Is(err, ErrSpendingGuardReauth) {
			t.Fatalf("relaxed policy without approval: %+v %v", next, err)
		}
	}
	if err := s.updateOnchainGuard(onchainGuardSettings{Enabled: true, MaxSendSat: 500, Rolling24hLimitSat: 3000}, false); err != nil {
		t.Fatal("tightening should not require a password", err)
	}
}

func TestOnchainGuardStorageFailureFailsClosed(t *testing.T) {
	s := onchainGuardFixture(t)
	if err := s.updateOnchainGuard(onchainGuardSettings{Enabled: true, MaxSendSat: 1000}, false); err != nil {
		t.Fatal(err)
	}
	s.onchainGuardPersistHook = func() error { return errors.New("injected storage failure") }
	id, binding := onchainGuardBinding(t, 100)
	if _, err := s.reserveOnchainGuard(id, binding, ""); err == nil {
		t.Fatal("reservation accepted without durability")
	}
	s.onchainGuardPersistHook = nil
	if _, err := s.onchainGuardStatus(); !errors.Is(err, errOnchainGuardUnavailable) {
		t.Fatal("continued with potentially stale cached policy")
	}
}

func TestOnchainGuardRejectsCorruptState(t *testing.T) {
	s := onchainGuardFixture(t)
	if err := os.WriteFile(filepath.Join(s.dir, onchainGuardFile), []byte(`{"version":1,"settings":{"enabled":true},"entries":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.onchainGuardStatus(); !errors.Is(err, errOnchainGuardUnavailable) {
		t.Fatal("invalid enabled state silently disabled guard")
	}
}

func TestOnchainGuardSweepUsesBoundThenNativeDestinationOnly(t *testing.T) {
	s := onchainGuardFixture(t)
	if err := s.updateOnchainGuard(onchainGuardSettings{Enabled: true, Rolling24hLimitSat: 10000}, false); err != nil {
		t.Fatal(err)
	}
	binding, transaction := financialNativeTransactionFixture(t)
	id, _ := newFinancialIntentID()
	binding.NativeLabel = "los-intent:" + id
	transaction.Label = binding.NativeLabel
	binding.Summary.AmountMode = "selected_maximum"
	binding.Summary.SelectedInputSat = 10000
	if _, err := s.reserveOnchainGuard(id, binding, ""); err != nil {
		t.Fatal(err)
	}
	if status, _ := s.onchainGuardStatus(); status.ReservedSat != 10000 {
		t.Fatal("sweep trusted an estimated fee")
	}
	amount, err := onchainGuardTransactionAmount(binding, transaction)
	if err != nil || amount != 1000 {
		t.Fatalf("change/mining fee counted as outgoing: %d %v", amount, err)
	}
	if err := s.finishOnchainGuard(id, transaction.GetTxHash(), amount, false); err != nil {
		t.Fatal(err)
	}
	if status, _ := s.onchainGuardStatus(); status.UsedSat != 1000 || status.ReservedSat != 0 || status.RemainingSat != 9000 {
		t.Fatalf("sweep fee/change not released: %+v", status)
	}
}

func TestOnchainGuardNativeDispatchBlockedAndReplyReplay(t *testing.T) {
	for _, limit := range []int64{500, 1000} {
		s, old, session := financialHTTPFixture(t)
		_, _ = s.financialIntents.cancelDraft(old.ID)
		if err := s.financialIntents.updateOnchainGuard(onchainGuardSettings{Enabled: true, MaxSendSat: limit, Rolling24hLimitSat: 2000}, false); err != nil {
			t.Fatal(err)
		}
		binding, transaction := financialNativeTransactionFixture(t)
		id, _ := newFinancialIntentID()
		binding.NativeLabel = "los-intent:" + id
		request := walletSendRequest{Address: binding.Summary.Destination, AmountSat: 1000, SatPerVbyte: 2, Outpoints: []string{binding.Inputs[0].Outpoint}}
		payload, _ := json.Marshal(request)
		snapshot, _ := json.Marshal(binding)
		record, err := s.financialIntents.create(id, session.ID, "/api/wallet/send", authScopeWalletSendExternal, payload, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		s.auth.sessions[session.ID].ReauthScopes[financialIntentScopePrefix+id] = record.ExpiresAt
		var sends atomic.Int64
		s.financialNativeOps = &financialNativeOperations{sendOnchain: func(context.Context, lndclient.SendCoinsParams) (string, error) {
			sends.Add(1)
			return transaction.GetTxHash(), nil
		}, restoreLabel: func(context.Context, string, string) error { return nil }}
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			txid, err := s.executeFinancialOnchainIntent(r.Context(), r, request)
			if err != nil {
				if !writeOnchainGuardError(w, err) {
					t.Error(err)
					writeError(w, 503, "send failed")
				}
				return
			}
			writeJSON(w, 200, map[string]string{"txid": txid})
		})
		for i := 0; i < 2; i++ {
			w := financialHTTPRequest(s, handler, session, id, string(payload), context.Background())
			want := 200
			if limit == 500 {
				want = 429
			}
			if w.Code != want {
				t.Fatalf("limit=%d status=%d body=%s", limit, w.Code, w.Body.String())
			}
		}
		want := int64(1)
		if limit == 500 {
			want = 0
		}
		if sends.Load() != want {
			t.Fatalf("limit=%d sends=%d", limit, sends.Load())
		}
	}
}

func TestOnchainGuardPolicyHTTPAndIntentScope(t *testing.T) {
	s, _, session := financialHTTPFixture(t)
	policy := `{"enabled":true,"max_send_sat":1000,"rolling_24h_limit_sat":5000}`
	if err := s.financialIntents.updateOnchainGuard(onchainGuardSettings{Enabled: true, MaxSendSat: 500, Rolling24hLimitSat: 1000}, false); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "https://node.example/api/wallet/onchain-spending-guard", strings.NewReader(policy))
	req.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: session.ID})
	req.Header.Set("Origin", "https://node.example")
	req.Header.Set("X-CSRF-Token", session.CSRFToken)
	w := httptest.NewRecorder()
	s.auth.Middleware()(s.financialIntentMiddleware(http.HandlerFunc(s.handleOnchainSpendingGuardUpdate))).ServeHTTP(w, req)
	if w.Code != 428 || !strings.Contains(w.Body.String(), "onchain_spending_guard_reauth_required") {
		t.Fatalf("policy relaxation bypassed exact approval: %d %s", w.Code, w.Body.String())
	}
	status, _ := s.financialIntents.onchainGuardStatus()
	if status.MaxSendSat != 500 {
		t.Fatal("unapproved policy applied")
	}
	id, _ := newFinancialIntentID()
	payload, binding, err := s.prepareAuxFinancialIntent(context.Background(), "/api/wallet/onchain-spending-guard", []byte(policy))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := json.Marshal(binding)
	record, err := s.financialIntents.create(id, session.ID, "/api/wallet/onchain-spending-guard", authScopeOnchainGuardPolicy, payload, snapshot, http.MethodPut)
	if err != nil {
		t.Fatal(err)
	}
	s.auth.sessions[session.ID].ReauthScopes[financialIntentScopePrefix+id] = record.ExpiresAt
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest(http.MethodPut, "https://node.example/api/wallet/onchain-spending-guard", strings.NewReader(string(payload)))
		r.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: session.ID})
		r.Header.Set("Origin", "https://node.example")
		r.Header.Set("X-CSRF-Token", session.CSRFToken)
		r.Header.Set(financialIntentHeader, id)
		response := httptest.NewRecorder()
		s.auth.Middleware()(s.financialIntentMiddleware(http.HandlerFunc(s.handleOnchainSpendingGuardUpdate))).ServeHTTP(response, r)
		if response.Code != 200 {
			t.Fatalf("approved exact policy/replay failed: %d %s", response.Code, response.Body.String())
		}
	}
	if updated, _ := s.financialIntents.onchainGuardStatus(); updated.MaxSendSat != 1000 || updated.Rolling24hLimitSat != 5000 {
		t.Fatal("approved policy not applied")
	}
	operation, covered := coreFinancialOperation("/api/wallet/onchain-spending-guard")
	if !covered || operation.scope != authScopeOnchainGuardPolicy || financialOperationMethod(operation) != http.MethodPut || !financialOperationConditional(operation) {
		t.Fatal("wrong policy intent scope/method")
	}
}

func TestOnchainGuardLostNativeReplyReconcilesWithoutAnotherSend(t *testing.T) {
	s, old, session := financialHTTPFixture(t)
	_, _ = s.financialIntents.cancelDraft(old.ID)
	if err := s.financialIntents.updateOnchainGuard(onchainGuardSettings{Enabled: true, Rolling24hLimitSat: 1000}, false); err != nil {
		t.Fatal(err)
	}
	binding, transaction := financialNativeTransactionFixture(t)
	id, _ := newFinancialIntentID()
	binding.NativeLabel = "los-intent:" + id
	transaction.Label = binding.NativeLabel
	transaction.NumConfirmations = 1
	request := walletSendRequest{Address: binding.Summary.Destination, AmountSat: 1000, SatPerVbyte: 2, Outpoints: []string{binding.Inputs[0].Outpoint}}
	payload, _ := json.Marshal(request)
	snapshot, _ := json.Marshal(binding)
	record, err := s.financialIntents.create(id, session.ID, "/api/wallet/send", authScopeWalletSendExternal, payload, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	s.auth.sessions[session.ID].ReauthScopes[financialIntentScopePrefix+id] = record.ExpiresAt
	var sends atomic.Int64
	s.financialNativeOps = &financialNativeOperations{
		sendOnchain: func(context.Context, lndclient.SendCoinsParams) (string, error) {
			sends.Add(1)
			return "", errors.New("native reply lost")
		},
		findOnchain: func(context.Context, string, int32) ([]*lnrpc.Transaction, error) {
			return []*lnrpc.Transaction{transaction}, nil
		},
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := s.executeFinancialOnchainIntent(r.Context(), r, request)
		if err != nil {
			writeError(w, 503, "uncertain")
		}
	})
	for i := 0; i < 2; i++ {
		financialHTTPRequest(s, handler, session, id, string(payload), context.Background())
	}
	if sends.Load() != 1 {
		t.Fatal("lost reply caused another native send")
	}
	if status, _ := s.financialIntents.onchainGuardStatus(); status.ReservedSat != 1000 || status.UsedSat != 0 {
		t.Fatal("lost reply released quota")
	}
	latest, _, _ := s.financialIntents.get(id)
	for i := 0; i < 2; i++ {
		if status, _, err := s.observeFinancialIntent(context.Background(), latest); err != nil || status != 200 {
			t.Fatalf("reconciliation failed: %d %v", status, err)
		}
	}
	if status, _ := s.financialIntents.onchainGuardStatus(); status.ReservedSat != 0 || status.UsedSat != 1000 {
		t.Fatalf("reconciliation miscounted: %+v", status)
	}
	if sends.Load() != 1 {
		t.Fatal("reconciliation resubmitted")
	}
}
