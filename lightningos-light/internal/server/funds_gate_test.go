package server

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"io"
	"lightningos-light/internal/config"
	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"
	"lightningos-light/lnrpc/routerrpc"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fundsGateLightning struct {
	lnrpc.UnimplementedLightningServer
}

func (*fundsGateLightning) DecodePayReq(context.Context, *lnrpc.PayReqString) (*lnrpc.PayReq, error) {
	return &lnrpc.PayReq{NumSatoshis: 100, NumMsat: 100000, PaymentHash: strings.Repeat("ab", 32)}, nil
}

type fundsGateRouter struct {
	routerrpc.UnimplementedRouterServer
	sends atomic.Int64
}

func (r *fundsGateRouter) SendPaymentV2(*routerrpc.SendPaymentRequest, routerrpc.Router_SendPaymentV2Server) error {
	r.sends.Add(1)
	return status.Error(codes.PermissionDenied, "simulated rejection; no funds exist")
}
func newFundsGateLND(t *testing.T) (*lndclient.Client, *fundsGateRouter) {
	t.Helper()
	cert := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer cert.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	router := &fundsGateRouter{}
	rpc := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: cert.TLS.Certificates, MinVersion: tls.VersionTLS12})))
	lnrpc.RegisterLightningServer(rpc, &fundsGateLightning{})
	routerrpc.RegisterRouterServer(rpc, router)
	go rpc.Serve(listener)
	t.Cleanup(rpc.Stop)
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	macPath := filepath.Join(dir, "test.macaroon")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(macPath, []byte("test-placeholder"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.LND.GRPCHost = listener.Addr().String()
	cfg.LND.TLSCertPath = certPath
	cfg.LND.AdminMacaroonPath = macPath
	c := lndclient.New(cfg, log.New(io.Discard, "", 0))
	t.Cleanup(func() { c.Close() })
	return c, router
}

func TestChatFundsRequireReauthAndAvailableGuard(t *testing.T) {
	client, rpc := newFundsGateLND(t)
	logger := log.New(io.Discard, "", 0)
	auth := NewAuthService(&config.Config{}, logger)
	session, err := auth.newSession()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{auth: auth, cfg: &config.Config{}, logger: logger, lnd: client, chat: NewChatService(client, logger), spendingGuardInitAt: time.Now(), spendingGuardErr: "test unavailable", auditLogInitAt: time.Now()}
	call := func(intentIDs ...string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://node.example/api/chat/send", strings.NewReader(`{"peer_pubkey":"02`+strings.Repeat("11", 32)+`","message":"test","amount_sat":100}`))
		r.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: session.ID})
		r.Header.Set("X-CSRF-Token", session.CSRFToken)
		r.Header.Set("Origin", "https://node.example")
		if len(intentIDs) > 0 {
			r.Header.Set(financialIntentHeader, intentIDs[0])
		}
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, r)
		return w
	}
	rec := call()
	if rec.Code != 428 || rpc.sends.Load() != 0 {
		t.Fatalf("unapproved chat reached payment: HTTP %d sends %d", rec.Code, rpc.sends.Load())
	}
	auth.mu.Lock()
	auth.sessions[session.ID].ReauthScopes[authScopeLightningFunds] = time.Now().Add(time.Minute)
	auth.mu.Unlock()
	rec = call()
	if rec.Code != 428 || rpc.sends.Load() != 0 {
		t.Fatal("broad Lightning grant authorized chat without its intent")
	}
	payload := []byte(`{"peer_pubkey":"02` + strings.Repeat("11", 32) + `","message":"test","amount_sat":100}`)
	_, binding, err := prepareChatFinancialIntent(payload)
	if err != nil {
		t.Fatal(err)
	}
	intent := financialHandlerApproval(t, s, session, "/api/chat/send", payload, binding)
	rec = call(intent.ID)
	if rec.Code != 503 || rpc.sends.Load() != 0 {
		t.Fatalf("unavailable guard allowed chat: HTTP %d sends %d", rec.Code, rpc.sends.Load())
	}
	for _, path := range []string{"/api/wallet/pay", "/api/wallet/pay/validated-route", "/api/wallet/pay/mpp"} {
		r := httptest.NewRequest("POST", "https://node.example"+path, strings.NewReader(`{"payment_request":"test-invoice"}`))
		r.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: session.ID})
		r.Header.Set("X-CSRF-Token", session.CSRFToken)
		r.Header.Set("Origin", "https://node.example")
		intent := financialHandlerApproval(t, s, session, path, []byte(`{"payment_request":"test-invoice"}`), financialIntentBinding{Kind: "invoice_payment"})
		r.Header.Set(financialIntentHeader, intent.ID)
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, r)
		if w.Code != 503 || rpc.sends.Load() != 0 {
			t.Fatalf("%s allowed unavailable policy: %d sends %d", path, w.Code, rpc.sends.Load())
		}
	}
}

func TestMissingSpendingGuardBlocksInvoiceAndChat(t *testing.T) {
	client, rpc := newFundsGateLND(t)
	logger := log.New(io.Discard, "", 0)
	s := &Server{lnd: client, logger: logger, spendingGuardInitAt: time.Now(), spendingGuardErr: "test unavailable"}
	_, _, err := s.reserveInvoiceSpending(context.Background(), "test-invoice", 100, 1, "wallet")
	if !errors.Is(err, ErrSpendingGuardUnavailable) {
		t.Fatalf("invoice allowed without policy: %v", err)
	}
	_, err = NewChatService(client, logger).SendMessage(context.Background(), "02"+strings.Repeat("11", 32), 100, "test")
	if !errors.Is(err, ErrSpendingGuardUnavailable) || rpc.sends.Load() != 0 {
		t.Fatalf("chat allowed without policy: %v, sends %d", err, rpc.sends.Load())
	}
}
