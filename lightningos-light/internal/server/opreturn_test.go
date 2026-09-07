package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"
)

func TestOPReturnAuthAndCSRF(t *testing.T) {
	auth := NewAuthService(nil, nil)
	session := &authSession{ID: "session", CSRFToken: "csrf", ExpiresAt: time.Now().Add(time.Hour)}
	auth.sessions[session.ID] = session
	for _, tc := range []struct {
		name, cookie, csrf string
		want               int
	}{{"anonymous", "", "", 401}, {"missing csrf", "session", "", 403}, {"wrong csrf", "session", "wrong", 403}, {"valid", "session", "csrf", 204}} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "https://localhost/api/apps/opreturn/publish", strings.NewReader(`{}`))
			r.Header.Set("Origin", "https://localhost")
			r.Header.Set("X-CSRF-Token", tc.csrf)
			if tc.cookie != "" {
				r.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: tc.cookie})
			}
			w := httptest.NewRecorder()
			auth.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
		})
	}
	if !authScopeValid("opreturn_publish") || auth.HasRecentReauth("session", "opreturn_publish") {
		t.Fatal("fresh reauth required")
	}
	session.ReauthScopes = map[string]time.Time{"opreturn_publish": time.Now().Add(-time.Second)}
	if auth.HasRecentReauth("session", "opreturn_publish") {
		t.Fatal("expired reauth accepted")
	}
	for _, handler := range []func(*Server, http.ResponseWriter, *http.Request){(*Server).handleOPReturnStatus, (*Server).handleOPReturnPreview, (*Server).handleOPReturnPublish, (*Server).handleOPReturnRecords} {
		w := httptest.NewRecorder()
		handler(&Server{}, w, httptest.NewRequest("GET", "/", nil))
		if w.Code != 401 {
			t.Fatal("route did not fail closed")
		}
	}
}

func TestOPReturnExactJSONUnicode(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"text":"\ud83d\ude00"}`, true}, {`{"text":"\\ud800"}`, true},
		{`{"text":"\ud800"}`, false}, {`{"text":"\udc00"}`, false}, {`{"text":"\ud800\u0041"}`, false},
		{"{\"text\":\"\xff\"}", false}, {`{"text":"a"} {}`, false},
	} {
		var out struct {
			Text string `json:"text"`
		}
		w := httptest.NewRecorder()
		if got := decodeOPReturn(w, httptest.NewRequest("POST", "/", strings.NewReader(tc.body)), &out); got != tc.valid {
			t.Fatalf("unicode decoding valid=%v wanted=%v", got, tc.valid)
		}
	}
}

type opreturnTestWallet struct {
	quote                        lndclient.OPReturnQuote
	funds, publishes, releases   int
	timeout, known, finalizeFail bool
}

func (w *opreturnTestWallet) Fund(_ context.Context, text string, rate int64) (opreturnFunding, error) {
	w.funds++
	w.quote.Text = text
	w.quote.SatPerVbyte = rate
	return opreturnTestFunding{w}, nil
}
func (w *opreturnTestWallet) PublishTransaction(context.Context, string, string) error {
	w.publishes++
	if w.timeout {
		return context.DeadlineExceeded
	}
	w.known = true
	return nil
}
func (w *opreturnTestWallet) OPReturnTransaction(context.Context, string) (*lnrpc.Transaction, error) {
	if !w.known {
		return nil, errors.New("not observed")
	}
	return &lnrpc.Transaction{TxHash: strings.Repeat("a", 64), NumConfirmations: 1, BlockHeight: 123}, nil
}

type opreturnTestFunding struct{ w *opreturnTestWallet }

func (f opreturnTestFunding) Quote() lndclient.OPReturnQuote { return f.w.quote }
func (f opreturnTestFunding) Release() error                 { f.w.releases++; return nil }
func (f opreturnTestFunding) Finalize(context.Context) (string, string, error) {
	if f.w.finalizeFail {
		return "", "", errors.New("finalization failed")
	}
	return strings.Repeat("a", 64), "00", nil
}

// This test is opt-in and all tables are restricted to a session-local pg_temp
// namespace. It cannot access or modify the running app's publication tables.
func TestOPReturnPostgresLifecycleAndPublication(t *testing.T) {
	dsn := os.Getenv("LIGHTNINGOS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("LIGHTNINGOS_TEST_POSTGRES_DSN not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	cfg.MaxConns = 1
	cfg.MinConns = 0
	cfg.ConnConfig.RuntimeParams["search_path"] = "pg_temp"
	ctx := context.Background()
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	auth := NewAuthService(nil, nil)
	auth.sessions["s"] = &authSession{ID: "s", ExpiresAt: time.Now().Add(time.Hour), ReauthScopes: map[string]time.Time{"opreturn_publish": time.Now().Add(time.Minute)}}
	wallet := &opreturnTestWallet{quote: lndclient.OPReturnQuote{FeeSat: 200, TotalDebitSat: 200, ByteCount: 4}}
	s := &Server{db: db, auth: auth, opreturnWalletClient: wallet, logger: log.New(io.Discard, "", 0)}
	app := opreturnApp{s}
	if err = app.Install(ctx); err != nil {
		t.Fatal(err)
	}
	if info, e := app.Info(ctx); e != nil || !info.Installed || info.Status != "running" {
		t.Fatal(info, e)
	}
	if err = app.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if s.opreturnActive(ctx) == nil {
		t.Fatal("stopped app active")
	}
	if info, _ := app.Info(ctx); !info.Installed {
		t.Fatal("stopping removed installed state")
	}
	if err = app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	call := func(handler func(http.ResponseWriter, *http.Request), body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://localhost/api/apps/opreturn/publish", strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), authSessionContextKey, authSessionSnapshot{ID: "s"}))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	preview := func() opreturnPreview {
		w := call(s.handleOPReturnPreview, `{"text":"test","sat_per_vbyte":1}`)
		if w.Code != 200 {
			t.Fatalf("preview %d %s", w.Code, w.Body)
		}
		var p opreturnPreview
		if err = json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	body := func(id, key string) string {
		raw, _ := json.Marshal(map[string]any{"preview_id": id, "idempotency_key": key, "confirm_publication": true})
		return string(raw)
	}
	p := preview()
	if _, err = db.Exec(ctx, `UPDATE opreturn_previews SET expires_at=now()-interval '1 second' WHERE id=$1`, p.PreviewID); err != nil {
		t.Fatal(err)
	}
	if w := call(s.handleOPReturnPublish, body(p.PreviewID, "expired-key-12345")); w.Code != 409 {
		t.Fatal("expired preview accepted")
	}
	p = preview()
	if _, err = db.Exec(ctx, `UPDATE opreturn_previews SET session_hash='another-session' WHERE id=$1`, p.PreviewID); err != nil {
		t.Fatal(err)
	}
	if w := call(s.handleOPReturnPublish, body(p.PreviewID, "session-key-12345")); w.Code != 409 {
		t.Fatal("cross-session preview accepted")
	}
	// Reset instrumentation after the extra preview capability checks.
	wallet.funds = 0
	wallet.releases = 0
	p = preview()
	delete(auth.sessions["s"].ReauthScopes, "opreturn_publish")
	if w := call(s.handleOPReturnPublish, body(p.PreviewID, "reauth-key-123456")); w.Code != 403 {
		t.Fatal("publish without reauth", w.Code)
	}
	auth.sessions["s"].ReauthScopes["opreturn_publish"] = time.Now().Add(time.Minute)
	for i := 0; i < 2; i++ {
		w := call(s.handleOPReturnPublish, body(p.PreviewID, "idempotent-key-123"))
		if w.Code != 200 {
			t.Fatalf("publish %d %s", w.Code, w.Body)
		}
	}
	if wallet.publishes != 1 || wallet.funds != 2 || wallet.releases != 2 {
		t.Fatalf("duplicate transaction or lease leak: %+v", wallet)
	}
	if w := call(s.handleOPReturnPublish, body(p.PreviewID, "another-key-12345")); w.Code != 409 {
		t.Fatal("preview replay accepted")
	}
	p = preview()
	wallet.quote.FeeSat = 201
	if w := call(s.handleOPReturnPublish, body(p.PreviewID, "fee-cap-key-12345")); w.Code != 409 {
		t.Fatal("fee cap not enforced", w.Body)
	}
	if wallet.publishes != 1 || wallet.releases != 4 {
		t.Fatal("fee cap broadcast or lease leak")
	}
	wallet.quote.FeeSat = 200
	p = preview()
	wallet.finalizeFail = true
	if w := call(s.handleOPReturnPublish, body(p.PreviewID, "finalize-key-1234")); w.Code != 409 {
		t.Fatal("finalization failure ignored")
	}
	wallet.finalizeFail = false
	if wallet.publishes != 1 || wallet.releases != 6 {
		t.Fatal("finalization broadcast or lease leak")
	}
	p = preview()
	wallet.timeout = true
	wallet.known = false
	for i := 0; i < 2; i++ {
		w := call(s.handleOPReturnPublish, body(p.PreviewID, "timeout-key-12345"))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
	}
	if wallet.publishes != 2 || wallet.releases != 8 {
		t.Fatal("timeout double broadcast or lease leak")
	}
	restarted := &Server{db: db, auth: auth, opreturnWalletClient: wallet, logger: log.New(io.Discard, "", 0)}
	if w := call(restarted.handleOPReturnPublish, body(p.PreviewID, "timeout-key-12345")); w.Code != 200 || wallet.publishes != 2 {
		t.Fatal("restart repeated unknown publication")
	}
	// A different key, preview or process must also fail closed on uncertainty.
	newPreview := preview()
	if w := call(s.handleOPReturnPublish, body(newPreview.PreviewID, "new-after-timeout")); w.Code != 409 {
		t.Fatal("unknown broadcast allowed another publication")
	}
	wallet.known = true
	w := call(s.handleOPReturnPublish, body(p.PreviewID, "timeout-key-12345"))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var rec opreturnRecord
	_ = json.Unmarshal(w.Body.Bytes(), &rec)
	if rec.State != "confirmed" || rec.Confirmations != 1 || rec.BlockHeight != 123 || wallet.publishes != 2 {
		t.Fatal("reconciliation failed", rec)
	}
	var qraw []byte
	if err = db.QueryRow(ctx, `SELECT quote FROM opreturn_previews WHERE id=$1`, newPreview.PreviewID).Scan(&qraw); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO opreturn_records(id,session_hash,idempotency_key,preview_id,quote,state,created_at) VALUES('interrupted','s','interrupted-key',$1,$2,'preparing',now()-interval '4 minutes')`, newPreview.PreviewID, qraw); err != nil {
		t.Fatal(err)
	}
	s.recoverOPReturnPreparations(ctx)
	interrupted, err := s.opreturnRecord(ctx, "interrupted")
	if err != nil || interrupted.State != "failed" {
		t.Fatal("interrupted preparation did not recover")
	}
	barrier, err := db.Exec(ctx, `UPDATE opreturn_records SET state='unknown' WHERE id='interrupted' AND state='preparing'`)
	if err != nil || barrier.RowsAffected() != 0 {
		t.Fatal("recovered preparation crossed broadcast barrier")
	}
	if err = app.Uninstall(ctx); err != nil {
		t.Fatal(err)
	}
	if info, _ := app.Info(ctx); info.Installed {
		t.Fatal("uninstall remained installed")
	}
	if s.opreturnActive(ctx) == nil {
		t.Fatal("uninstalled app active")
	}
	if _, err = s.opreturnRecord(ctx, rec.ID); err != nil {
		t.Fatal("uninstall lost history")
	}
}
