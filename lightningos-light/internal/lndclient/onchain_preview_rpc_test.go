package lndclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/pem"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"lightningos-light/internal/config"
	"lightningos-light/lnrpc"
	"lightningos-light/lnrpc/walletrpc"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type previewKitFixture struct {
	walletrpc.UnimplementedWalletKitServer
	owned, watched *lnrpc.Utxo
	requests       []*walletrpc.FundPsbtRequest
	released       int
}
type previewLightningFixture struct {
	lnrpc.UnimplementedLightningServer
	kit *previewKitFixture
}

func (f *previewLightningFixture) ListUnspent(context.Context, *lnrpc.ListUnspentRequest) (*lnrpc.ListUnspentResponse, error) {
	return &lnrpc.ListUnspentResponse{Utxos: []*lnrpc.Utxo{f.kit.owned, f.kit.watched}}, nil
}
func (f *previewKitFixture) ListUnspent(_ context.Context, r *walletrpc.ListUnspentRequest) (*walletrpc.ListUnspentResponse, error) {
	if r.Account != "default" {
		return &walletrpc.ListUnspentResponse{Utxos: []*lnrpc.Utxo{f.owned, f.watched}}, nil
	}
	return &walletrpc.ListUnspentResponse{Utxos: []*lnrpc.Utxo{f.owned}}, nil
}
func (f *previewKitFixture) FundPsbt(_ context.Context, r *walletrpc.FundPsbtRequest) (*walletrpc.FundPsbtResponse, error) {
	f.requests = append(f.requests, r)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(10000, []byte{0x51}))
	tx.AddTxOut(wire.NewTxOut(89000, []byte{0x51}))
	packet, _ := psbt.NewFromUnsignedTx(tx)
	var b bytes.Buffer
	_ = packet.Serialize(&b)
	return &walletrpc.FundPsbtResponse{FundedPsbt: b.Bytes(), ChangeOutputIndex: 1, LockedUtxos: []*walletrpc.UtxoLease{{Id: make([]byte, 32), Outpoint: f.owned.Outpoint, Value: 100000, PkScript: []byte{0x51}}}}, nil
}
func (f *previewKitFixture) ReleaseOutput(context.Context, *walletrpc.ReleaseOutputRequest) (*walletrpc.ReleaseOutputResponse, error) {
	f.released++
	return &walletrpc.ReleaseOutputResponse{}, nil
}
func TestPreviewDefaultAccountAndExplicitSelection(t *testing.T) {
	for _, mode := range []string{"automatic", "selected", "watch-only", "sweep"} {
		t.Run(mode, func(t *testing.T) {
			f := &previewKitFixture{owned: &lnrpc.Utxo{AmountSat: 100000, Outpoint: &lnrpc.OutPoint{TxidStr: strings.Repeat("00", 32)}}, watched: &lnrpc.Utxo{AmountSat: 900000, Outpoint: &lnrpc.OutPoint{TxidStr: strings.Repeat("11", 32)}}}
			tlsFixture := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			defer tlsFixture.Close()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: tlsFixture.TLS.Certificates, MinVersion: tls.VersionTLS12})))
			lnrpc.RegisterLightningServer(server, &previewLightningFixture{kit: f})
			walletrpc.RegisterWalletKitServer(server, f)
			go server.Serve(listener)
			defer server.Stop()
			dir := t.TempDir()
			cert := filepath.Join(dir, "tls.crt")
			mac := filepath.Join(dir, "fixture.macaroon")
			if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsFixture.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(mac, []byte("fictional-fixture-macaroon"), 0600); err != nil {
				t.Fatal(err)
			}
			c := New(&config.Config{LND: config.LNDConfig{GRPCHost: listener.Addr().String(), TLSCertPath: cert, AdminMacaroonPath: mac}}, nil)
			defer c.Close()
			var selected []string
			if mode == "selected" {
				selected = []string{strings.Repeat("00", 32) + ":0"}
			}
			if mode == "watch-only" {
				selected = []string{strings.Repeat("11", 32) + ":0"}
			}
			got, err := c.PreviewOnchainSend(context.Background(), "1BoatSLRHtKNngkdXEeobR76b53LETtpyT", 10000, 2, mode == "sweep", selected)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "watch-only" {
				if got.EnoughFunds || len(f.requests) != 0 {
					t.Fatal("watched input accepted")
				}
				return
			}
			if got.SpendableSat != 100000 {
				t.Fatalf("preview includes non-default account funds: %d", got.SpendableSat)
			}
			if mode == "sweep" {
				if len(f.requests) != 0 {
					t.Fatal("sweep created leases")
				}
				return
			}
			if len(f.requests) != 1 || f.released != 1 {
				t.Fatal("funding/release count")
			}
			inputs := len(f.requests[0].GetRaw().Inputs)
			if mode == "automatic" && inputs != 0 {
				t.Fatalf("automatic preview forced %d inputs", inputs)
			}
			if mode == "selected" && inputs != 1 {
				t.Fatal("selection not preserved")
			}
		})
	}
}

type previewReleaseFixture struct {
	walletrpc.WalletKitClient
	called bool
	usable bool
}

func (f *previewReleaseFixture) ReleaseOutput(ctx context.Context, _ *walletrpc.ReleaseOutputRequest, _ ...grpc.CallOption) (*walletrpc.ReleaseOutputResponse, error) {
	f.called = true
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 4*time.Second {
		return nil, context.DeadlineExceeded
	}
	f.usable = true
	return &walletrpc.ReleaseOutputResponse{}, nil
}
func TestPreviewReleaseSurvivesRequestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &previewReleaseFixture{}
	releasePreviewLeases(ctx, f, []*walletrpc.UtxoLease{{Id: make([]byte, 32), Outpoint: &lnrpc.OutPoint{}}})
	if !f.called || !f.usable {
		t.Fatal("release omitted or used the canceled request context")
	}
}
