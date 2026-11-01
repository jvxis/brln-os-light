package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcutil/bech32"
	"lightningos-light/internal/lndclient"
)

func withdrawCode(t *testing.T, raw string) string {
	t.Helper()
	data, err := bech32.ConvertBits([]byte(raw), 8, 5, true)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := bech32.Encode("lnurl", data)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// HTTPS fixture retains normal certificate verification, routing only the
// test transport to a locally trusted test service. Production blocks loopback.
func withdrawMerchant(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	merchant := httptest.NewTLSServer(handler)
	t.Cleanup(merchant.Close)
	original := http.DefaultTransport
	transport := original.(*http.Transport).Clone()
	transport.DialTLSContext = func(context.Context, string, string) (net.Conn, error) {
		return tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", strings.TrimPrefix(merchant.URL, "https://"), merchant.Client().Transport.(*http.Transport).TLSClientConfig)
	}
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = original; transport.CloseIdleConnections() })
}

func TestLNURLWithdrawDecodeAndSafety(t *testing.T) {
	good := withdrawCode(t, "https://voucher.example/withdraw/secret")
	for _, code := range []string{good, strings.ToUpper(good), "lightning:" + good} {
		u, err := decodeWithdrawLNURL(code)
		if err != nil || u.Host != "voucher.example" {
			t.Fatalf("valid code rejected: %v", err)
		}
	}
	for _, raw := range []string{"http://voucher.example/x", "https://127.0.0.1/x", "https://[::1]/x", "https://10.0.0.1/x", "https://100.64.0.1/x", "https://169.254.169.254/x", "https://localhost/x", "https://a.localhost/x", "https://user:secret@voucher.example/x", "https://voucher.example/x#fragment"} {
		if _, err := decodeWithdrawLNURL(withdrawCode(t, raw)); err == nil {
			t.Errorf("unsafe URL accepted: %s", raw)
		}
	}
	for _, bad := range []string{good[:len(good)-1] + "!", strings.ToUpper(good[:8]) + good[8:], strings.Repeat("a", 7001), "https://voucher.example/x"} {
		if _, err := decodeWithdrawLNURL(bad); err == nil {
			t.Error("invalid input accepted")
		}
	}
	// The existing pay resolver and the new withdraw resolver share this client.
	// Exercise actual resolution/dial rejection, not just URL text validation.
	client := safeLNURLClient()
	if resp, err := client.Get("https://127.0.0.1:1/private"); err == nil {
		resp.Body.Close()
		t.Fatal("loopback dial allowed")
	}
	redirect, _ := http.NewRequest("GET", "http://voucher.example/x", nil)
	if client.CheckRedirect(redirect, nil) == nil {
		t.Fatal("HTTPS downgrade allowed")
	}
}

func TestLNURLWithdrawDescriptorLimits(t *testing.T) {
	base := lnurlWithdrawDescriptor{Tag: "withdrawRequest", Callback: "https://provider.example/callback", K1: "test-secret", Min: 1001, Max: 3999}
	_, min, max, err := validateWithdrawDescriptor(base)
	if err != nil || min != 2 || max != 3 {
		t.Fatalf("rounding inward failed: %d %d %v", min, max, err)
	}
	for _, mutate := range []func(*lnurlWithdrawDescriptor){
		func(d *lnurlWithdrawDescriptor) { d.Tag = "payRequest" },
		func(d *lnurlWithdrawDescriptor) { d.Tag = "login" },
		func(d *lnurlWithdrawDescriptor) { d.Min = -1 },
		func(d *lnurlWithdrawDescriptor) { d.Max = 0 },
		func(d *lnurlWithdrawDescriptor) { d.Min = 0; d.Max = 999 },
		func(d *lnurlWithdrawDescriptor) { d.Max = lnurlWithdrawMaxMsat + 1 },
		func(d *lnurlWithdrawDescriptor) { d.K1 = "" },
		func(d *lnurlWithdrawDescriptor) { d.Callback = "https://192.168.1.1/callback" },
		func(d *lnurlWithdrawDescriptor) { d.Status = "ERROR" },
	} {
		d := base
		mutate(&d)
		if _, _, _, err := validateWithdrawDescriptor(d); err == nil {
			t.Errorf("bad descriptor accepted: %+v", d)
		}
	}
}

func withdrawRequest(body, owner string) *http.Request {
	r := httptest.NewRequest("POST", "/api/wallet/lnurl-withdraw", strings.NewReader(body))
	return r.WithContext(context.WithValue(r.Context(), authSessionContextKey, authSessionSnapshot{ID: owner}))
}

func TestLNURLWithdrawPreviewAndSessionBinding(t *testing.T) {
	var calls atomic.Int64
	withdrawMerchant(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		json.NewEncoder(w).Encode(lnurlWithdrawDescriptor{Tag: "withdrawRequest", Callback: "https://provider.example/callback/private", K1: "secret-k1", Min: 1000, Max: 3000, Description: "Receive test coins"})
	})
	s := &Server{}
	body, _ := json.Marshal(map[string]string{"lnurl": withdrawCode(t, "https://provider.example/voucher/private-token")})
	w := httptest.NewRecorder()
	s.handleLNURLWithdrawPreview(w, withdrawRequest(string(body), "alice"))
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	for _, secret := range []string{"secret-k1", "private-token", "/callback/private"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("preview leaked provider credentials")
		}
	}
	var preview struct {
		Token string `json:"token"`
	}
	json.Unmarshal(w.Body.Bytes(), &preview)
	if len(preview.Token) != 64 || calls.Load() != 1 {
		t.Fatal("invalid ticket or unexpected callback")
	}
	for _, tc := range []struct {
		owner  string
		amount int
		code   int
	}{{"bob", 2, 400}, {"alice", 0, 400}, {"alice", 4, 400}} {
		w = httptest.NewRecorder()
		s.handleLNURLWithdraw(w, withdrawRequest(fmt.Sprintf(`{"token":%q,"amount_sat":%d}`, preview.Token, tc.amount), tc.owner))
		if w.Code != tc.code {
			t.Fatalf("expected rejection: %d", w.Code)
		}
	}
	entry := s.lnurlWithdrawals[preview.Token]
	entry.amountSat = 2
	w = httptest.NewRecorder()
	s.handleLNURLWithdraw(w, withdrawRequest(fmt.Sprintf(`{"token":%q,"amount_sat":3}`, preview.Token), "alice"))
	if w.Code != 400 {
		t.Fatal("changed amount accepted on retry")
	}
	entry.expires = time.Now().Add(-time.Second)
	w = httptest.NewRecorder()
	s.handleLNURLWithdraw(w, withdrawRequest(fmt.Sprintf(`{"token":%q,"amount_sat":2}`, preview.Token), "alice"))
	if w.Code != 400 || calls.Load() != 1 {
		t.Fatal("expired/invalid requests reached external callback")
	}
}

type withdrawCreator struct {
	calls int
	fail  bool
}

func (c *withdrawCreator) CreateInvoice(_ context.Context, amount int64, memo string, expiry int64, opts *lndclient.CreateInvoiceOptions) (lndclient.CreatedInvoice, error) {
	c.calls++
	if c.fail {
		return lndclient.CreatedInvoice{}, errors.New("secret RPC failure")
	}
	if amount != 2 || memo != "LNURL withdrawal" || expiry != 600 || opts != nil {
		return lndclient.CreatedInvoice{}, errors.New("unexpected invoice request")
	}
	return lndclient.CreatedInvoice{PaymentRequest: "lnbc-test-receiving-invoice", PaymentHash: strings.Repeat("ab", 32)}, nil
}

func TestLNURLWithdrawRetryUsesSameInvoice(t *testing.T) {
	var callbacks atomic.Int64
	withdrawMerchant(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("k1") != "test-secret" || r.URL.Query().Get("pr") != "lnbc-test-receiving-invoice" || r.URL.Query().Get("keep") != "yes" {
			t.Error("callback parameters changed")
		}
		switch callbacks.Add(1) {
		case 1:
			w.Write([]byte(`{"status":"ERROR","reason":"secret-k1"}`))
		case 2:
			w.Write([]byte(`not-json`))
		default:
			w.Write([]byte(`{"status":"OK"}`))
		}
	})
	callback, _ := url.Parse("https://provider.example/callback?keep=yes&k1=replace&pr=replace")
	entry := &lnurlWithdrawal{callback: callback, k1: "test-secret"}
	creator := &withdrawCreator{}
	for i, want := range []string{"rejected", "unknown", "accepted", "accepted"} {
		created, contacted, err := entry.redeem(context.Background(), creator, 2)
		if err != nil || entry.status != want || created != (i == 0) || contacted != (i < 3) {
			t.Fatalf("attempt %d: created=%v contacted=%v status=%s err=%v", i, created, contacted, entry.status, err)
		}
	}
	if creator.calls != 1 || callbacks.Load() != 3 {
		t.Fatal("retry created duplicate invoice or repeated accepted request")
	}
}

func TestLNURLWithdrawErrorsDoNotLeakVoucher(t *testing.T) {
	withdrawMerchant(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat("x", 32769))) })
	endpoint, _ := url.Parse("https://provider.example/secret-voucher?k1=secret-k1")
	var target any
	err := withdrawJSON(context.Background(), endpoint, &target)
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("oversized response accepted or secret leaked")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	err = withdrawJSON(canceled, endpoint, &target)
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("URL-bearing transport error leaked")
	}
	creator := &withdrawCreator{fail: true}
	entry := &lnurlWithdrawal{callback: endpoint}
	_, contacted, err := entry.redeem(context.Background(), creator, 2)
	if err == nil || contacted || strings.Contains(err.Error(), "secret") {
		t.Fatal("invoice failure leaked or contacted callback")
	}
}

func TestLNURLWithdrawRequiresAuthAndCSRF(t *testing.T) {
	now := time.Now()
	auth := &AuthService{enabled: true, now: func() time.Time { return now }, sessions: map[string]*authSession{"session-id": {ID: "session-id", CSRFToken: "csrf-token", ExpiresAt: now.Add(time.Hour)}}}
	for _, path := range []string{"/api/wallet/lnurl-withdraw/preview", "/api/wallet/lnurl-withdraw"} {
		for _, tc := range []struct {
			cookie, csrf string
			want         int
		}{{"", "", 401}, {"session-id", "", 403}, {"session-id", "csrf-token", 204}} {
			r := httptest.NewRequest("POST", "https://los.example"+path, strings.NewReader(`{}`))
			r.Header.Set("Origin", "https://los.example")
			if tc.cookie != "" {
				r.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: tc.cookie})
			}
			r.Header.Set("X-CSRF-Token", tc.csrf)
			w := httptest.NewRecorder()
			auth.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("%s: wanted %d got %d %s", path, tc.want, w.Code, w.Body.String())
			}
		}
	}
}

func TestLNURLWithdrawConcurrentRetryAndTicketCapacity(t *testing.T) {
	var callbacks atomic.Int64
	withdrawMerchant(t, func(w http.ResponseWriter, r *http.Request) {
		callbacks.Add(1)
		if r.URL.Path == "/callback" {
			w.Write([]byte(`{"status":"OK"}`))
			return
		}
		json.NewEncoder(w).Encode(lnurlWithdrawDescriptor{Tag: "withdrawRequest", Callback: "https://provider.example/callback", K1: "test", Min: 1000, Max: 3000})
	})
	callback, _ := url.Parse("https://provider.example/callback")
	entry := &lnurlWithdrawal{callback: callback, k1: "test", minSat: 1, maxSat: 3}
	creator := &withdrawCreator{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			entry.mu.Lock()
			defer entry.mu.Unlock()
			if _, _, err := entry.redeem(context.Background(), creator, 2); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if creator.calls != 1 || callbacks.Load() != 1 {
		t.Fatal("concurrent retry duplicated invoice/callback")
	}
	s := &Server{lnurlWithdrawals: make(map[string]*lnurlWithdrawal)}
	for i := 0; i < 64; i++ {
		s.lnurlWithdrawals[fmt.Sprint(i)] = &lnurlWithdrawal{expires: time.Now().Add(time.Hour)}
	}
	body, _ := json.Marshal(map[string]string{"lnurl": withdrawCode(t, "https://provider.example/voucher")})
	w := httptest.NewRecorder()
	s.handleLNURLWithdrawPreview(w, withdrawRequest(string(body), "alice"))
	if w.Code != 429 || len(s.lnurlWithdrawals) != 64 {
		t.Fatal("ticket store grew beyond limit")
	}
	s.lnurlWithdrawals["0"].expires = time.Now().Add(-time.Second)
	w = httptest.NewRecorder()
	s.handleLNURLWithdrawPreview(w, withdrawRequest(string(body), "alice"))
	if w.Code != 200 || len(s.lnurlWithdrawals) != 64 {
		t.Fatal("expired ticket was not reclaimed")
	}
}
