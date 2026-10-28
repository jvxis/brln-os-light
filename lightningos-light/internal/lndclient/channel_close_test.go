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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"lightningos-light/internal/config"
	"lightningos-light/lnrpc"
)

type channelCloseFixture struct {
	lnrpc.UnimplementedLightningServer
	point, observedPoint, txid, location, failure string
	readFailure                                   bool
	calls                                         atomic.Int32
}

func (f *channelCloseFixture) CloseChannel(_ *lnrpc.CloseChannelRequest, _ lnrpc.Lightning_CloseChannelServer) error {
	f.calls.Add(1)
	return status.Error(codes.Unknown, f.failure)
}

func (f *channelCloseFixture) PendingChannels(context.Context, *lnrpc.PendingChannelsRequest) (*lnrpc.PendingChannelsResponse, error) {
	if f.readFailure {
		return nil, status.Error(codes.Unavailable, "read unavailable")
	}
	r := &lnrpc.PendingChannelsResponse{}
	if f.calls.Load() > 0 && f.location == "pending" {
		r.WaitingCloseChannels = []*lnrpc.PendingChannelsResponse_WaitingCloseChannel{{Channel: &lnrpc.PendingChannelsResponse_PendingChannel{ChannelPoint: f.observedPoint}, ClosingTxid: f.txid}}
	}
	return r, nil
}

func (f *channelCloseFixture) ClosedChannels(context.Context, *lnrpc.ClosedChannelsRequest) (*lnrpc.ClosedChannelsResponse, error) {
	if f.readFailure {
		return nil, status.Error(codes.Unavailable, "read unavailable")
	}
	r := &lnrpc.ClosedChannelsResponse{}
	if f.calls.Load() > 0 && f.location == "closed" {
		r.Channels = []*lnrpc.ChannelCloseSummary{{ChannelPoint: f.observedPoint, ClosingTxHash: f.txid}}
	}
	return r, nil
}

func closeTestClient(t *testing.T, fixture *channelCloseFixture) *Client {
	t.Helper()
	tlsFixture := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(tlsFixture.Close)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: tlsFixture.TLS.Certificates, MinVersion: tls.VersionTLS12})))
	lnrpc.RegisterLightningServer(server, fixture)
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	dir := t.TempDir()
	cert, mac := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "test.macaroon")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsFixture.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mac, []byte("fixture-not-a-real-macaroon"), 0600); err != nil {
		t.Fatal(err)
	}
	client := New(&config.Config{LND: config.LNDConfig{GRPCHost: listener.Addr().String(), TLSCertPath: cert, AdminMacaroonPath: mac}}, nil)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestCloseChannelReconcilesErrorWithoutAnotherSubmission(t *testing.T) {
	point := strings.Repeat("1", 64) + ":1"
	for _, tc := range []struct{ name, location, failure string }{
		{"pending after offline error", "pending", "channel link not found"},
		{"confirmed after offline error", "closed", "channel link not found"},
		{"broadcast before fee error", "pending", "latest fee proposal exceeds max fee"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &channelCloseFixture{point: point, observedPoint: point, txid: strings.Repeat("2", 64), location: tc.location, failure: tc.failure}
			client := closeTestClient(t, f)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			txid, err := client.CloseChannel(ctx, point, false, 2)
			if err != nil || txid != f.txid {
				t.Fatalf("native close was lost: txid=%q err=%v", txid, err)
			}
			if f.calls.Load() != 1 {
				t.Fatalf("close resubmitted after broadcast: %d", f.calls.Load())
			}
		})
	}
}

func TestCloseChannelPreservesUnconfirmedErrors(t *testing.T) {
	point := strings.Repeat("1", 64) + ":1"
	for _, tc := range []struct {
		name, observedPoint, location string
		readFailure                   bool
	}{
		{"offline without a close", point, "", false},
		{"another channel pending", strings.Repeat("3", 64) + ":0", "pending", false},
		{"observation unavailable", point, "pending", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &channelCloseFixture{point: point, observedPoint: tc.observedPoint, location: tc.location, txid: strings.Repeat("2", 64), failure: "channel link not found", readFailure: tc.readFailure}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			txid, err := closeTestClient(t, f).CloseChannel(ctx, point, false, 2)
			if txid != "" || err == nil || !strings.Contains(err.Error(), f.failure) {
				t.Fatalf("unproven close accepted: %q %v", txid, err)
			}
			if f.calls.Load() != 1 {
				t.Fatal("unexpected second submission")
			}
		})
	}
}
