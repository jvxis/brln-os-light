package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"lightningos-light/internal/config"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Handler-isolation fixture: preparation/native integration is exercised by
// separate API and regtest tests; these cases preserve downstream contracts.
func financialHandlerApproval(t *testing.T, s *Server, session authSessionSnapshot, path string, payload []byte, binding financialIntentBinding) *financialIntentRecord {
	t.Helper()
	if s.financialIntents == nil {
		store, err := openFinancialIntentStore(financialPrivateTestDir(t))
		if err != nil {
			t.Fatal(err)
		}
		s.financialIntents = store
		t.Cleanup(func() { store.Close() })
	}
	id, _ := newFinancialIntentID()
	if binding.Kind == "onchain_send" {
		binding.NativeLabel = "los-intent:" + id
	}
	snapshot, _ := json.Marshal(binding)
	operation, ok := coreFinancialOperation(path)
	if !ok {
		t.Fatal("fixture operation not covered")
	}
	record, err := s.financialIntents.create(id, session.ID, path, operation.scope, payload, snapshot, financialOperationMethod(operation))
	if err != nil {
		t.Fatal(err)
	}
	s.auth.mu.Lock()
	s.auth.sessions[session.ID].ReauthScopes[financialIntentScopePrefix+id] = record.ExpiresAt
	s.auth.mu.Unlock()
	return record
}

func financialHTTPFixture(t *testing.T) (*Server, *financialIntentRecord, authSessionSnapshot) {
	t.Helper()
	store, err := openFinancialIntentStore(financialPrivateTestDir(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	auth := NewAuthService(nil, log.New(io.Discard, "", 0))
	session, err := auth.newSession()
	if err != nil {
		t.Fatal(err)
	}
	id, _ := newFinancialIntentID()
	record, err := store.create(id, session.ID, "/api/wallet/send", authScopeWalletSendExternal, []byte(`{"address":"fixture-destination","amount_sat":1000,"sat_per_vbyte":2}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{auth: auth, financialIntents: store, logger: log.New(io.Discard, "", 0), auditLogInitAt: time.Now()}
	auth.sessions[session.ID].ReauthScopes[financialIntentScopePrefix+id] = time.Now().Add(time.Minute)
	return s, record, session
}

func financialHTTPRequest(s *Server, handler http.Handler, session authSessionSnapshot, id, body string, ctx context.Context) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "https://node.example/api/wallet/send", strings.NewReader(body))
	r = r.WithContext(ctx)
	r.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: session.ID})
	r.Header.Set("Origin", "https://node.example")
	r.Header.Set("X-CSRF-Token", session.CSRFToken)
	if id != "" {
		r.Header.Set(financialIntentHeader, id)
	}
	w := httptest.NewRecorder()
	s.auth.Middleware()(s.financialIntentMiddleware(handler)).ServeHTTP(w, r)
	return w
}

func TestFinancialIntentHTTPReplayAndPayloadBinding(t *testing.T) {
	s, record, session := financialHTTPFixture(t)
	var calls atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !financialIntentApproved(r, authScopeWalletSendExternal) || financialIntentApproved(r, authScopeLightningFunds) {
			t.Error("wrong financial scope context")
		}
		writeJSON(w, 200, map[string]string{"txid": "known-transaction"})
	})
	for _, body := range []string{
		`{"address":"changed","amount_sat":1000,"sat_per_vbyte":2}`,
		`{"address":"fixture-destination","amount_sat":9000,"sat_per_vbyte":2}`,
		`{"address":"fixture-destination","amount_sat":1000,"sat_per_vbyte":20}`,
	} {
		if w := financialHTTPRequest(s, handler, session, record.ID, body, context.Background()); w.Code != 409 {
			t.Fatal("changed transfer reached execution")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unapproved changed payload reached handler")
	}
	w := financialHTTPRequest(s, handler, session, record.ID, string(record.Request), context.Background())
	if w.Code != 200 || calls.Load() != 1 || s.auth.HasRecentReauth(session.ID, financialIntentScopePrefix+record.ID) {
		t.Fatal("exact approval was not consumed once")
	}
	s.financialIntents.now = func() time.Time { return record.ExpiresAt.Add(time.Hour) }
	retry := financialHTTPRequest(s, handler, session, record.ID, string(record.Request), context.Background())
	if retry.Code != 200 || retry.Header().Get("X-LOS-Financial-Replay") != "true" || retry.Body.String() != w.Body.String() || calls.Load() != 1 {
		t.Fatal("lost-response retry duplicated execution or lost cached result")
	}
	if !strings.Contains(retry.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("financial replay response is cacheable")
	}
	other, err := s.auth.newSession()
	if err != nil {
		t.Fatal(err)
	}
	if w := financialHTTPRequest(s, handler, other, record.ID, string(record.Request), context.Background()); w.Code != 409 || calls.Load() != 1 {
		t.Fatal("different session inherited an intent")
	}
	s.auth.logout(session.ID)
	if w := financialHTTPRequest(s, handler, session, record.ID, string(record.Request), context.Background()); w.Code != 401 || calls.Load() != 1 {
		t.Fatal("logout did not revoke access")
	}
}

func TestFinancialIntentHTTPLostConnectionAndConcurrency(t *testing.T) {
	s, record, session := financialHTTPFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	finish := make(chan struct{})
	var calls atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(entered)
		<-finish
		if r.Context().Err() != nil {
			t.Error("client cancellation abandoned committed financial operation")
		}
		writeJSON(w, 200, map[string]string{"txid": "completed-after-disconnect"})
	})
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- financialHTTPRequest(s, handler, session, record.ID, string(record.Request), ctx) }()
	<-entered
	cancel()
	concurrent := financialHTTPRequest(s, handler, session, record.ID, string(record.Request), context.Background())
	if concurrent.Code != 409 || !strings.Contains(concurrent.Body.String(), "financial_intent_pending") || calls.Load() != 1 {
		t.Fatal("concurrent retry reached native execution")
	}
	close(finish)
	if (<-result).Code != 200 {
		t.Fatal("committed operation did not finish")
	}
	if retry := financialHTTPRequest(s, handler, session, record.ID, string(record.Request), context.Background()); retry.Code != 200 || calls.Load() != 1 {
		t.Fatal("disconnected operation was executed twice")
	}
}

func TestFinancialIntentHTTPStorageFailureNeverDispatches(t *testing.T) {
	s, record, session := financialHTTPFixture(t)
	s.financialIntents.persistHook = func(*financialIntentRecord) error { return errors.New("fixture disk full") }
	var calls atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	if w := financialHTTPRequest(s, handler, session, record.ID, string(record.Request), context.Background()); w.Code != 503 || calls.Load() != 0 {
		t.Fatal("failed durable claim dispatched a financial action")
	}
}

func TestFinancialIntentHTTPUncertainOutcomeNeverResubmits(t *testing.T) {
	for _, panicAfterDispatch := range []bool{false, true} {
		s, record, session := financialHTTPFixture(t)
		var calls atomic.Int64
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if panicAfterDispatch {
				panic("fixture interrupted after dispatch")
			}
			writeError(w, 500, "native result unavailable")
		})
		func() {
			defer func() {
				if recovered := recover(); recovered != nil && !panicAfterDispatch {
					t.Error("unexpected execution panic")
				}
			}()
			financialHTTPRequest(s, handler, session, record.ID, string(record.Request), context.Background())
		}()
		retry := financialHTTPRequest(s, handler, session, record.ID, string(record.Request), context.Background())
		if retry.Code != 409 || !strings.Contains(retry.Body.String(), "financial_intent_pending") || calls.Load() != 1 {
			t.Fatal("uncertain financial action was blindly resubmitted")
		}
		stored, running, _ := s.financialIntents.get(record.ID)
		if running || stored.State != financialIntentUncertainState {
			t.Fatal("uncertain financial operation misrepresented as completed")
		}
	}
}

func TestFinancialIntentGatePreservesReadPreviewsAndExcludedApps(t *testing.T) {
	s, _, _ := financialHTTPFixture(t)
	var calls atomic.Int64
	handler := s.financialIntentMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	for _, path := range []string{"/api/wallet/send/preview", "/api/wallet/pay/preview", "/api/lnops/channel/open-preview", "/api/lnops/channel/open-batch-preview", "/api/wallet/decode", "/api/apps/tapd/send"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader("{}")))
		if w.Code != 204 {
			t.Fatal("read preview or excluded app contract changed")
		}
	}
	if calls.Load() != 6 {
		t.Fatal("preview handler skipped")
	}
}

func TestFinancialIntentEncodedSessionCannotBypassApproval(t *testing.T) {
	s, _, session := financialHTTPFixture(t)
	s.auth.sessions[session.ID].ReauthScopes[authScopeLightningFunds] = time.Now().Add(time.Minute)
	s.balancedOpenInitAt = time.Now()
	s.balancedOpenErr = "fixture unavailable"
	s.cfg = &config.Config{}
	for _, id := range []string{"plain-id", "encoded%2Fsession", "encoded%2fsession"} {
		path := "/api/lnops/balanced-open/sessions/" + id + "/propose"
		r := httptest.NewRequest("POST", "https://node.example"+path, strings.NewReader(`{}`))
		r.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: session.ID})
		r.Header.Set("Origin", "https://node.example")
		r.Header.Set("X-CSRF-Token", session.CSRFToken)
		w := httptest.NewRecorder()
		// Real chi routing uses RawPath for these encoded parameters.
		s.routes().ServeHTTP(w, r)
		if w.Code != 428 || !strings.Contains(w.Body.String(), "requires_financial_intent") {
			t.Errorf("encoded route bypassed intent gate: %s HTTP %d", id, w.Code)
		}
	}
}
