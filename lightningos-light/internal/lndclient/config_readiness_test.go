package lndclient

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"lightningos-light/internal/config"
	"lightningos-light/lnrpc"
)

type configStateFixture struct {
	lnrpc.UnimplementedStateServer
	state atomic.Int32
}

func (f *configStateFixture) GetState(context.Context, *lnrpc.GetStateRequest) (*lnrpc.GetStateResponse, error) {
	return &lnrpc.GetStateResponse{State: lnrpc.WalletState(f.state.Load())}, nil
}

type configInfoFixture struct {
	lnrpc.UnimplementedLightningServer
	fail  atomic.Bool
	calls atomic.Int32
}

func (f *configInfoFixture) GetInfo(context.Context, *lnrpc.GetInfoRequest) (*lnrpc.GetInfoResponse, error) {
	f.calls.Add(1)
	if f.fail.Load() {
		return nil, status.Error(codes.Unavailable, "fixture is not ready")
	}
	return &lnrpc.GetInfoResponse{}, nil
}

func TestConfigReadinessBypassesHealthyCaches(t *testing.T) {
	tlsFixture := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer tlsFixture.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	state := &configStateFixture{}
	info := &configInfoFixture{}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: tlsFixture.TLS.Certificates, MinVersion: tls.VersionTLS12})))
	lnrpc.RegisterStateServer(server, state)
	lnrpc.RegisterLightningServer(server, info)
	go server.Serve(listener)
	defer server.Stop()
	dir := t.TempDir()
	cert, mac := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "test.macaroon")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsFixture.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mac, []byte("fixture-not-a-real-macaroon"), 0600); err != nil {
		t.Fatal(err)
	}
	client := New(&config.Config{LND: config.LNDConfig{GRPCHost: listener.Addr().String(), TLSCertPath: cert, AdminMacaroonPath: mac}}, nil)
	defer client.Close()
	client.storeRuntimeInfo(&lnrpc.GetInfoResponse{Alias: "cached", SyncedToChain: true, SyncedToGraph: true})
	state.state.Store(int32(lnrpc.WalletState_LOCKED))
	if got, err := client.ProbeConfigState(context.Background()); err != nil || got != "locked" {
		t.Fatalf("stale cached health accepted: %q %v", got, err)
	}
	state.state.Store(int32(lnrpc.WalletState_SERVER_ACTIVE))
	info.fail.Store(true)
	if _, err := client.ProbeConfigState(context.Background()); err == nil {
		t.Fatal("active state without a successful fresh GetInfo accepted")
	}
	info.fail.Store(false)
	if got, err := client.ProbeConfigState(context.Background()); err != nil || got != "active" || info.calls.Load() != 2 {
		t.Fatalf("fresh readiness not checked: %q %v", got, err)
	}
}
