package server

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"io"
	"lightningos-light/internal/config"
	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"
	"lightningos-light/lnrpc/routerrpc"
	"log"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const lnurlTestMetadata = `[["text/plain","test merchant"]]`

type lnurlInvoiceRPC struct {
	lnrpc.UnimplementedLightningServer
	mu      sync.Mutex
	decoded *lnrpc.PayReq
}

func (r *lnurlInvoiceRPC) DecodePayReq(context.Context, *lnrpc.PayReqString) (*lnrpc.PayReq, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return proto.Clone(r.decoded).(*lnrpc.PayReq), nil
}

type lnurlPaymentRPC struct {
	routerrpc.UnimplementedRouterServer
	sends atomic.Int64
}

func (r *lnurlPaymentRPC) SendPaymentV2(*routerrpc.SendPaymentRequest, routerrpc.Router_SendPaymentV2Server) error {
	r.sends.Add(1)
	return status.Error(codes.PermissionDenied, "simulated payment rejection")
}
func lnurlInvoiceClient(t *testing.T) (*lndclient.Client, *lnurlInvoiceRPC, *lnurlPaymentRPC) {
	t.Helper()
	cert := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer cert.Close()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	commitment := sha256.Sum256([]byte(lnurlTestMetadata))
	rpc := &lnurlInvoiceRPC{decoded: &lnrpc.PayReq{NumSatoshis: 100, NumMsat: 100000, PaymentHash: strings.Repeat("ab", 32), DescriptionHash: hex.EncodeToString(commitment[:]), Timestamp: time.Now().Unix(), Expiry: 3600}}
	pay := &lnurlPaymentRPC{}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: cert.TLS.Certificates, MinVersion: tls.VersionTLS12})))
	lnrpc.RegisterLightningServer(server, rpc)
	routerrpc.RegisterRouterServer(server, pay)
	go server.Serve(lis)
	t.Cleanup(server.Stop)
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	macPath := filepath.Join(dir, "test.macaroon")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(macPath, []byte("test-only"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.LND.GRPCHost = lis.Addr().String()
	cfg.LND.TLSCertPath = certPath
	cfg.LND.AdminMacaroonPath = macPath
	client := lndclient.New(cfg, log.New(io.Discard, "", 0))
	t.Cleanup(func() { client.Close() })
	return client, rpc, pay
}
func lnurlInvoiceMerchant(t *testing.T, invoice func() string) *atomic.Int64 {
	t.Helper()
	calls := &atomic.Int64{}
	merchant := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/.well-known/") {
			json.NewEncoder(w).Encode(map[string]any{"callback": "https://merchant.example/callback", "minSendable": 100000, "maxSendable": 100000, "tag": "payRequest", "metadata": lnurlTestMetadata, "commentAllowed": 100})
			return
		}
		if r.URL.Query().Get("amount") != "100000" {
			t.Error("requested amount changed")
		}
		calls.Add(1)
		json.NewEncoder(w).Encode(map[string]string{"pr": invoice()})
	}))
	t.Cleanup(merchant.Close)
	original := http.DefaultTransport
	transport := original.(*http.Transport).Clone()
	transport.DialTLSContext = func(context.Context, string, string) (net.Conn, error) {
		return tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", strings.TrimPrefix(merchant.URL, "https://"), merchant.Client().Transport.(*http.Transport).TLSClientConfig)
	}
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = original; transport.CloseIdleConnections() })
	return calls
}
func TestLNURLRejectsInconsistentInvoice(t *testing.T) {
	client, rpc, _ := lnurlInvoiceClient(t)
	rpc.decoded.NumSatoshis = 900
	rpc.decoded.NumMsat = 900000
	lnurlInvoiceMerchant(t, func() string { return "lnbc1test" })
	s := &Server{lnd: client}
	invoice, _, err := s.resolveWalletPaymentInput(context.Background(), "alice@merchant.example", 100, "", "", nil)
	if err == nil || invoice != "" {
		t.Fatalf("receiver substituted a different amount: invoice=%q err=%v", invoice, err)
	}
}

func TestLNURLInvoiceValidation(t *testing.T) {
	now := time.Now()
	commitment := sha256.Sum256([]byte(lnurlTestMetadata))
	base := lndclient.DecodedInvoice{AmountSat: 100, AmountMsat: 100000, Timestamp: now.Unix(), Expiry: 3600, DescriptionHash: hex.EncodeToString(commitment[:])}
	for _, tt := range []struct {
		name, invoice, metadata string
		change                  func(*lndclient.DecodedInvoice)
		valid                   bool
	}{
		{"valid", "lnbc1test", lnurlTestMetadata, nil, true},
		{"uppercase mainnet", "LNBC1TEST", lnurlTestMetadata, nil, true},
		{"larger", "lnbc1test", lnurlTestMetadata, func(d *lndclient.DecodedInvoice) { d.AmountMsat++ }, false},
		{"smaller", "lnbc1test", lnurlTestMetadata, func(d *lndclient.DecodedInvoice) { d.AmountMsat-- }, false},
		{"amountless", "lnbc1test", lnurlTestMetadata, func(d *lndclient.DecodedInvoice) { d.AmountMsat = 0; d.AmountSat = 0 }, false},
		{"expired", "lnbc1test", lnurlTestMetadata, func(d *lndclient.DecodedInvoice) { d.Timestamp = now.Add(-time.Hour).Unix() }, false},
		{"negative expiry", "lnbc1test", lnurlTestMetadata, func(d *lndclient.DecodedInvoice) { d.Expiry = -1 }, false},
		{"overflow expiry", "lnbc1test", lnurlTestMetadata, func(d *lndclient.DecodedInvoice) { d.Expiry = math.MaxInt64 }, false},
		{"testnet", "lntb1test", lnurlTestMetadata, nil, false},
		{"regtest", "lnbcrt1test", lnurlTestMetadata, nil, false},
		{"missing hash", "lnbc1test", lnurlTestMetadata, func(d *lndclient.DecodedInvoice) { d.DescriptionHash = "" }, false},
		{"wrong hash", "lnbc1test", lnurlTestMetadata, func(d *lndclient.DecodedInvoice) { d.DescriptionHash = strings.Repeat("cd", 32) }, false},
		{"metadata serialization changed", "lnbc1test", lnurlTestMetadata + " ", nil, false},
		{"invalid metadata", "lnbc1test", "invalid", nil, false},
		{"missing plain text", "lnbc1test", `[["text/identifier","alice@merchant.example"]]`, nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			decoded := base
			if tt.change != nil {
				tt.change(&decoded)
			}
			err := validateLNURLInvoice(tt.invoice, decoded, tt.metadata, 100000, now)
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v err=%v", tt.valid, err)
			}
		})
	}
}

func TestLNURLResolutionStaysImmutableThroughExecution(t *testing.T) {
	client, _, _ := lnurlInvoiceClient(t)
	var merchantInvoice atomic.Value
	merchantInvoice.Store("lnbc1first")
	calls := lnurlInvoiceMerchant(t, func() string { return merchantInvoice.Load().(string) })
	s := &Server{lnd: client}
	ctx := context.WithValue(context.Background(), authSessionContextKey, authSessionSnapshot{ID: "first-session"})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			invoice, _, err := s.resolveWalletPaymentInput(ctx, "alice@merchant.example", 100, "hello", "", nil)
			if err != nil || invoice != "lnbc1first" {
				t.Errorf("resolution: %q %v", invoice, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("concurrent preview created different intents: callbacks=%d", calls.Load())
	}
	merchantInvoice.Store("lnbc1second")
	invoice, _, err := s.resolveWalletPaymentInput(ctx, "alice@merchant.example", 100, "hello", "", nil)
	if err != nil || invoice != "lnbc1first" || calls.Load() != 1 {
		t.Fatalf("execution resolved again behind approval: %q %v callbacks=%d", invoice, err, calls.Load())
	}
	other := context.WithValue(context.Background(), authSessionContextKey, authSessionSnapshot{ID: "other-session"})
	invoice, _, err = s.resolveWalletPaymentInput(other, "alice@merchant.example", 100, "hello", "", nil)
	if err != nil || invoice != "lnbc1second" || calls.Load() != 2 {
		t.Fatalf("session reused another session's intent: %q %v", invoice, err)
	}
	s.lnurlInvoiceMu.Lock()
	for key, item := range s.lnurlInvoices {
		item.expires = time.Now().Add(-time.Second)
		s.lnurlInvoices[key] = item
	}
	s.lnurlInvoiceMu.Unlock()
	invoice, _, err = s.resolveWalletPaymentInput(ctx, "alice@merchant.example", 100, "hello", "", nil)
	if err != nil || invoice != "lnbc1second" || calls.Load() != 3 {
		t.Fatalf("expired intent not refreshed: %q %v", invoice, err)
	}
}

func TestLNURLApprovalIsBoundToInvoiceAndLimits(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	auth := NewAuthService(&config.Config{}, logger)
	session, err := auth.newSession()
	if err != nil {
		t.Fatal(err)
	}
	address := "alice@merchant.example"
	invoice := "lnbc1first"
	scope := lnurlInvoiceScope(address, invoice, 100, 2, []uint64{1})
	auth.sessions[session.ID].ReauthScopes[scope] = time.Now().Add(time.Minute)
	auth.sessions[session.ID].ReauthScopes[authScopeLightningFunds] = time.Now().Add(time.Minute)
	s := &Server{auth: auth}
	r := httptest.NewRequest("POST", "/api/wallet/pay", nil)
	r = r.WithContext(context.WithValue(r.Context(), authSessionContextKey, session))
	if !s.requireResolvedLNURLReauth(httptest.NewRecorder(), r, address, invoice, 100, 2, []uint64{1}, "") {
		t.Fatal("matching approval rejected")
	}
	for _, tt := range []struct {
		invoice     string
		amount, fee int64
		channels    []uint64
	}{{"lnbc1changed", 100, 2, []uint64{1}}, {invoice, 101, 2, []uint64{1}}, {invoice, 100, 3, []uint64{1}}, {invoice, 100, 2, []uint64{2}}} {
		rec := httptest.NewRecorder()
		if s.requireResolvedLNURLReauth(rec, r, address, tt.invoice, tt.amount, tt.fee, tt.channels, "") || rec.Code != 428 {
			t.Fatalf("changed intent reused approval: %d", rec.Code)
		}
	}
	if !authScopeValid(scope) || authScopeValid(lnurlInvoiceScopePrefix+"bad") {
		t.Fatal("invalid invoice scope validation")
	}
}

func TestLNURLAllPaymentModesRejectMismatchAndRequestBoundApproval(t *testing.T) {
	client, rpc, pay := lnurlInvoiceClient(t)
	lnurlInvoiceMerchant(t, func() string { return "lnbc1test" })
	logger := log.New(io.Discard, "", 0)
	auth := NewAuthService(&config.Config{}, logger)
	session, err := auth.newSession()
	if err != nil {
		t.Fatal(err)
	}
	auth.sessions[session.ID].ReauthScopes[authScopeLightningFunds] = time.Now().Add(time.Minute)
	s := &Server{lnd: client, auth: auth, logger: logger, cfg: &config.Config{}}
	for _, path := range []string{"/api/wallet/pay", "/api/wallet/pay/validated-route", "/api/wallet/pay/mpp"} {
		t.Run(path, func(t *testing.T) {
			call := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "https://node.example"+path, strings.NewReader(`{"payment_request":"alice@merchant.example","amount_sat":100,"max_fee_sat":2}`))
				r.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: session.ID})
				r.Header.Set("X-CSRF-Token", session.CSRFToken)
				r.Header.Set("Origin", "https://node.example")
				w := httptest.NewRecorder()
				s.routes().ServeHTTP(w, r)
				return w
			}
			rpc.mu.Lock()
			rpc.decoded.NumMsat = 200000
			rpc.mu.Unlock()
			s.lnurlInvoices = nil
			rec := call()
			if rec.Code != 400 || pay.sends.Load() != 0 {
				t.Fatalf("mismatch reached payment: %d sends=%d", rec.Code, pay.sends.Load())
			}
			rpc.mu.Lock()
			rpc.decoded.NumMsat = 100000
			rpc.mu.Unlock()
			rec = call()
			if rec.Code != 428 || !strings.Contains(rec.Body.String(), lnurlInvoiceScopePrefix) || pay.sends.Load() != 0 {
				t.Fatalf("unbound grant authorized invoice: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}
