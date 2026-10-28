package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"io"
	"lightningos-light/internal/config"
	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"
	"lightningos-light/lnrpc/walletrpc"
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

type onchainReauthRPC struct {
	lnrpc.UnimplementedLightningServer
	sends         atomic.Int64
	reads         atomic.Int64
	last          *lnrpc.SendCoinsRequest
	restoredLabel string
}

func (rpc *onchainReauthRPC) ListUnspent(context.Context, *lnrpc.ListUnspentRequest) (*lnrpc.ListUnspentResponse, error) {
	rpc.reads.Add(1)
	return &lnrpc.ListUnspentResponse{}, nil
}

func (rpc *onchainReauthRPC) SendCoins(_ context.Context, req *lnrpc.SendCoinsRequest) (*lnrpc.SendCoinsResponse, error) {
	rpc.last = req
	rpc.sends.Add(1)
	return &lnrpc.SendCoinsResponse{Txid: strings.Repeat("ab", 32)}, nil
}

type onchainOwnershipRPC struct {
	walletrpc.UnimplementedWalletKitServer
	account string
	reads   *atomic.Int64
	onLabel func(string)
}

func (rpc *onchainOwnershipRPC) LabelTransaction(_ context.Context, request *walletrpc.LabelTransactionRequest) (*walletrpc.LabelTransactionResponse, error) {
	if rpc.onLabel != nil {
		rpc.onLabel(request.Label)
	}
	return &walletrpc.LabelTransactionResponse{}, nil
}

func (rpc *onchainOwnershipRPC) ListUnspent(_ context.Context, req *walletrpc.ListUnspentRequest) (*walletrpc.ListUnspentResponse, error) {
	if req.Account != "default" {
		return nil, status.Error(codes.InvalidArgument, "default funding account required")
	}
	if rpc.reads != nil {
		rpc.reads.Add(1)
	}
	return &walletrpc.ListUnspentResponse{}, nil
}

func (rpc *onchainOwnershipRPC) ListAddresses(context.Context, *walletrpc.ListAddressesRequest) (*walletrpc.ListAddressesResponse, error) {
	if rpc.account == "unavailable" {
		return nil, status.Error(codes.Unavailable, "ownership lookup unavailable")
	}
	accounts := []*walletrpc.AccountWithAddresses{}
	if rpc.account != "external" {
		accounts = append(accounts, &walletrpc.AccountWithAddresses{Name: rpc.account, Addresses: []*walletrpc.AddressProperty{{Address: "1BoatSLRHtKNngkdXEeobR76b53LETtpyT"}}})
	}
	return &walletrpc.ListAddressesResponse{AccountWithAddresses: accounts}, nil
}
func onchainReauthServer(t *testing.T, account string) (*Server, *onchainReauthRPC, authSessionSnapshot) {
	t.Helper()
	cert := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer cert.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	rpc := &onchainReauthRPC{}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: cert.TLS.Certificates, MinVersion: tls.VersionTLS12})))
	lnrpc.RegisterLightningServer(server, rpc)
	walletrpc.RegisterWalletKitServer(server, &onchainOwnershipRPC{account: account, reads: &rpc.reads, onLabel: func(label string) { rpc.restoredLabel = label }})
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	macPath := filepath.Join(dir, "fixture.macaroon")
	if err = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(macPath, []byte("test-placeholder-not-a-real-macaroon"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.LND.GRPCHost = listener.Addr().String()
	cfg.LND.TLSCertPath = certPath
	cfg.LND.AdminMacaroonPath = macPath
	logger := log.New(io.Discard, "", 0)
	client := lndclient.New(cfg, logger)
	t.Cleanup(func() { client.Close() })
	auth := NewAuthService(cfg, logger)
	session, err := auth.newSession()
	if err != nil {
		t.Fatal(err)
	}
	return &Server{cfg: cfg, auth: auth, lnd: client, logger: logger, auditLogInitAt: time.Now()}, rpc, session
}
func onchainReauthRequest(s *Server, session authSessionSnapshot, path, body string, csrf bool, intentIDs ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "https://node.example"+path, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: session.ID})
	req.Header.Set("Origin", "https://node.example")
	if len(intentIDs) > 0 {
		req.Header.Set(financialIntentHeader, intentIDs[0])
	}
	if csrf {
		req.Header.Set("X-CSRF-Token", session.CSRFToken)
	}
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	return rec
}
func TestWalletSendAllDestinationsRequireReauth(t *testing.T) {
	for _, account := range []string{"imported", "default", "external", "unavailable"} {
		t.Run(account, func(t *testing.T) {
			s, rpc, session := onchainReauthServer(t, account)
			body := `{"address":"1BoatSLRHtKNngkdXEeobR76b53LETtpyT","amount_sat":1000}`
			rec := onchainReauthRequest(s, session, "/api/wallet/send", body, true)
			if rec.Code != 428 || rpc.sends.Load() != 0 {
				t.Fatalf("unapproved transfer reached RPC: status=%d sends=%d", rec.Code, rpc.sends.Load())
			}
			var challenge struct {
				Code     string `json:"code"`
				Required bool   `json:"requires_password_confirmation"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &challenge); err != nil || !challenge.Required || challenge.Code != "wallet_send_external_reauth_required" {
				t.Fatal("challenge compatibility lost")
			}
			s.auth.mu.Lock()
			s.auth.sessions[session.ID].ReauthScopes[authScopeLightningFunds] = time.Now().Add(time.Minute)
			s.auth.mu.Unlock()
			for _, payload := range []string{body, `{"address":"1BoatSLRHtKNngkdXEeobR76b53LETtpyT","sweep_all":true}`, `{"address":"1BoatSLRHtKNngkdXEeobR76b53LETtpyT","amount_sat":1000,"label":"consolidation"}`} {
				rec = onchainReauthRequest(s, session, "/api/wallet/send", payload, true)
				if rec.Code != 428 || rpc.sends.Load() != 0 {
					t.Fatal("other scope or send mode bypassed approval")
				}
			}
			s.auth.mu.Lock()
			s.auth.sessions[session.ID].ReauthScopes[authScopeWalletSendExternal] = time.Now().Add(time.Minute)
			s.auth.mu.Unlock()
			rec = onchainReauthRequest(s, session, "/api/wallet/send", body, false)
			if rec.Code != 403 || rpc.sends.Load() != 0 {
				t.Fatal("CSRF bypass")
			}
			for _, payload := range []string{body, `{"address":"1BoatSLRHtKNngkdXEeobR76b53LETtpyT","sweep_all":true}`, `{"address":"1BoatSLRHtKNngkdXEeobR76b53LETtpyT","amount_sat":1000,"outpoints":["` + strings.Repeat("aa", 32) + `:1"],"label":"consolidation","sat_per_vbyte":2}`} {
				before := rpc.sends.Load()
				if blocked := onchainReauthRequest(s, session, "/api/wallet/send", payload, true); blocked.Code != 428 {
					t.Fatal("broad wallet grant bypassed exact approval")
				}
				var request walletSendRequest
				if json.Unmarshal([]byte(payload), &request) != nil {
					t.Fatal("invalid fixture")
				}
				if request.SatPerVbyte == 0 {
					request.SatPerVbyte = 2
				}
				if len(request.Outpoints) == 0 {
					request.Outpoints = []string{strings.Repeat("aa", 32) + ":1"}
				}
				encoded, _ := json.Marshal(request)
				inputs := []lndclient.FinancialIntentInput{{Outpoint: request.Outpoints[0], AmountSat: 100000}}
				binding := financialIntentBinding{Kind: "onchain_send", Inputs: inputs, Summary: financialIntentSummary{Destination: request.Address, AmountSat: request.AmountSat, SatPerVbyte: request.SatPerVbyte}}
				intent := financialHandlerApproval(t, s, session, "/api/wallet/send", encoded, binding)
				rec = onchainReauthRequest(s, session, "/api/wallet/send", string(intent.Request), true, intent.ID)
				if rec.Code != 200 || rpc.sends.Load() != before+1 {
					t.Fatalf("authorized send broken: HTTP %d", rec.Code)
				}
			}
			if !strings.HasPrefix(rpc.last.Label, "los-intent:") || rpc.restoredLabel != "consolidation" || len(rpc.last.Outpoints) != 1 || rpc.last.SatPerVbyte != 2 {
				t.Fatal("advanced send parameters changed")
			}
			s.auth.mu.Lock()
			s.auth.sessions[session.ID].ReauthScopes[authScopeWalletSendExternal] = time.Now().Add(-time.Second)
			s.auth.mu.Unlock()
			before := rpc.sends.Load()
			rec = onchainReauthRequest(s, session, "/api/wallet/send", body, true)
			if rec.Code != 428 || rpc.sends.Load() != before {
				t.Fatal("expired approval bypass")
			}
		})
	}
}
func TestWalletSendPreviewPreservesReadOnlyAndLoginDisabled(t *testing.T) {
	s, rpc, session := onchainReauthServer(t, "imported")
	// Valid preview performs a read-only UTXO query, never a broadcast.
	body := `{"address":"1BoatSLRHtKNngkdXEeobR76b53LETtpyT","amount_sat":1000,"sat_per_vbyte":2}`
	for _, enabled := range []bool{true, false} {
		s.auth.enabled = enabled
		rec := onchainReauthRequest(s, session, "/api/wallet/send/preview", body, true)
		var preview struct {
			Required bool `json:"requires_password_confirmation"`
		}
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &preview) != nil || preview.Required != enabled || rpc.sends.Load() != 0 {
			t.Fatalf("preview gating regression enabled=%v status=%d", enabled, rec.Code)
		}
	}
	if rpc.reads.Load() != 2 {
		t.Fatal("preview did not query wallet UTXOs")
	}
	rec := onchainReauthRequest(s, session, "/api/wallet/send", body, true)
	if rec.Code != 200 || rpc.sends.Load() != 1 {
		t.Fatal("explicitly disabled login behavior changed")
	}
}
