package server

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
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
	"sync"
	"testing"
)

type pendingRouterFixture struct {
	routerrpc.UnimplementedRouterServer
	mu         sync.Mutex
	payments   map[string]*lnrpc.Payment
	trackErr   error
	sends      int
	beforeSend func(*routerrpc.SendPaymentRequest) error
}

func (r *pendingRouterFixture) TrackPaymentV2(req *routerrpc.TrackPaymentRequest, stream routerrpc.Router_TrackPaymentV2Server) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.trackErr != nil {
		return r.trackErr
	}
	p := r.payments[hex.EncodeToString(req.PaymentHash)]
	if p == nil {
		return status.Error(codes.NotFound, "simulated missing record")
	}
	return stream.Send(p)
}
func (r *pendingRouterFixture) SendPaymentV2(req *routerrpc.SendPaymentRequest, _ routerrpc.Router_SendPaymentV2Server) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sends++
	if r.beforeSend != nil {
		if err := r.beforeSend(req); err != nil {
			return err
		}
	}
	return status.Error(codes.Unavailable, "simulated response lost after submission")
}
func pendingRPCClient(t *testing.T) (*lndclient.Client, *pendingRouterFixture) {
	t.Helper()
	cert := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer cert.Close()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	router := &pendingRouterFixture{payments: map[string]*lnrpc.Payment{}}
	rpc := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: cert.TLS.Certificates, MinVersion: tls.VersionTLS12})))
	routerrpc.RegisterRouterServer(rpc, router)
	lnrpc.RegisterLightningServer(rpc, &lnrpc.UnimplementedLightningServer{})
	go rpc.Serve(lis)
	t.Cleanup(rpc.Stop)
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
	c := lndclient.New(cfg, log.New(io.Discard, "", 0))
	t.Cleanup(func() { c.Close() })
	return c, router
}
func TestPendingSpendingWalletUncertainOutcomes(t *testing.T) {
	pool := pendingSpendingPool(t)
	ctx := context.Background()
	guard := NewSpendingGuardService(pool, nil)
	client, rpc := pendingRPCClient(t)
	s := &Server{spendingGuard: guard, lnd: client}
	for _, tt := range []struct {
		name     string
		err      error
		payment  *lnrpc.Payment
		trackErr error
		want     string
	}{
		{"EOF", io.EOF, nil, nil, "reserved"}, {"canceled", context.Canceled, nil, nil, "reserved"}, {"lost response", errors.New("response lost"), nil, nil, "reserved"},
		{"lookup unavailable", errors.New("send failed"), nil, status.Error(codes.Unavailable, "simulated"), "reserved"},
		{"inflight", errors.New("send failed"), &lnrpc.Payment{Status: lnrpc.Payment_IN_FLIGHT}, nil, "reserved"},
		{"confirmed failed", io.EOF, &lnrpc.Payment{Status: lnrpc.Payment_FAILED}, nil, "released"},
		{"confirmed succeeded", io.EOF, &lnrpc.Payment{Status: lnrpc.Payment_SUCCEEDED, FeeSat: 2}, nil, "settled"},
		{"success awaiting index", nil, nil, nil, "settled"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `truncate spending_guard_entries`); err != nil {
				t.Fatal(err)
			}
			hash := strings.Repeat("ab", 32)
			r, err := guard.Reserve(ctx, SpendingIntent{AmountSat: 80, MaxFeeSat: 20, PaymentHash: hash})
			if err != nil {
				t.Fatal(err)
			}
			rpc.mu.Lock()
			rpc.trackErr = tt.trackErr
			rpc.payments = map[string]*lnrpc.Payment{hash: tt.payment}
			rpc.mu.Unlock()
			s.finishSpendingReservation(r, hash, tt.err)
			var state string
			if err := pool.QueryRow(ctx, `select status from spending_guard_entries where id=$1`, r.ID).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state != tt.want {
				t.Fatalf("outcome %s, want %s", state, tt.want)
			}
		})
	}
}
func TestPendingSpendingChatPersistsHashBeforeRPC(t *testing.T) {
	pool := pendingSpendingPool(t)
	ctx := context.Background()
	guard := NewSpendingGuardService(pool, nil)
	client, rpc := pendingRPCClient(t)
	rpc.beforeSend = func(req *routerrpc.SendPaymentRequest) error {
		var bound bool
		err := pool.QueryRow(ctx, `select exists(select 1 from spending_guard_entries where payment_hash=$1 and status='reserved')`, hex.EncodeToString(req.PaymentHash)).Scan(&bound)
		if err != nil {
			return err
		}
		if !bound {
			t.Error("payment reached RPC before durable hash binding")
		}
		return nil
	}
	chat := NewChatService(client, log.New(io.Discard, "", 0))
	chat.AttachSpendingGuard(guard)
	_, err := chat.SendMessage(ctx, "02"+strings.Repeat("11", 32), 50, "test")
	if err == nil {
		t.Fatal("expected simulated lost response")
	}
	var state, hash string
	if err := pool.QueryRow(ctx, `select status,payment_hash from spending_guard_entries`).Scan(&state, &hash); err != nil {
		t.Fatal(err)
	}
	if state != "reserved" || len(hash) != 64 || rpc.sends != 1 {
		t.Fatalf("ambiguous Keysend lost its exposure: %s %q sends %d", state, hash, rpc.sends)
	}
	rpc.mu.Lock()
	rpc.payments[hash] = &lnrpc.Payment{Status: lnrpc.Payment_FAILED}
	rpc.mu.Unlock()
	s := &Server{spendingGuard: guard, lnd: client}
	s.reconcileSpendingGuard()
	s.reconcileSpendingGuard()
	st, err := guard.Status(ctx)
	if err != nil || st.ReservedSat != 0 || st.UsedSat != 0 {
		t.Fatalf("definitive failure not released idempotently: %+v %v", st, err)
	}
}
func TestPendingSpendingReconcilerRotatesUnresolvedBatch(t *testing.T) {
	pool := pendingSpendingPool(t)
	ctx := context.Background()
	guard := NewSpendingGuardService(pool, nil)
	client, rpc := pendingRPCClient(t)
	for i := 0; i < 105; i++ {
		hash := fmt.Sprintf("%064x", i+1)
		if i == 0 {
			hash = ""
		}
		if _, err := pool.Exec(ctx, `insert into spending_guard_entries(id,source,payment_hash,amount_sat,max_fee_sat,reserved_sat,status,created_at,updated_at) values($1,'wallet',$2,1,0,1,'reserved',now()-interval '49 hours',now()-interval '49 hours'+$3*interval '1 second')`, fmt.Sprint(i), hash, i); err != nil {
			t.Fatal(err)
		}
		if i >= 100 {
			rpc.payments[hash] = &lnrpc.Payment{Status: lnrpc.Payment_SUCCEEDED}
		}
	}
	s := &Server{spendingGuard: guard, lnd: client}
	s.reconcileSpendingGuard()
	s.reconcileSpendingGuard()
	st, err := guard.Status(ctx)
	if err != nil || st.UsedSat != 5 || st.ReservedSat != 100 {
		t.Fatalf("unresolved batch starved newer settlements: %+v %v", st, err)
	}
	var state string
	if err := pool.QueryRow(ctx, `select status from spending_guard_entries where id='0'`).Scan(&state); err != nil || state != "reserved" {
		t.Fatal("unbound legacy entry was released on age", state, err)
	}
}

type pendingBindFailure struct{ releases int }

func (g *pendingBindFailure) Reserve(context.Context, SpendingIntent) (SpendingReservation, error) {
	return SpendingReservation{ID: "test", Active: true, AmountSat: 1, ReservedSat: 2}, nil
}
func (g *pendingBindFailure) Bind(context.Context, SpendingReservation, string) error {
	return errors.New("simulated persistence failure")
}
func (g *pendingBindFailure) Release(context.Context, SpendingReservation, string) error {
	g.releases++
	return nil
}
func (g *pendingBindFailure) Settle(context.Context, SpendingReservation, int64, string) error {
	return errors.New("unexpected settlement")
}
func TestPendingSpendingBindFailurePreventsKeysend(t *testing.T) {
	client, rpc := pendingRPCClient(t)
	g := &pendingBindFailure{}
	chat := NewChatService(client, log.New(io.Discard, "", 0))
	chat.AttachSpendingGuard(g)
	_, err := chat.SendMessage(context.Background(), "02"+strings.Repeat("11", 32), 1, "test")
	if err == nil || rpc.sends != 0 || g.releases != 1 {
		t.Fatalf("binding failure submitted or stranded payment: sends=%d releases=%d err=%v", rpc.sends, g.releases, err)
	}
}
