package lndclient

import (
	"bytes"
	"context"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"lightningos-light/internal/config"
	"lightningos-light/lnrpc"
	"lightningos-light/lnrpc/routerrpc"
	"lightningos-light/lnrpc/walletrpc"
	"net"
	"testing"
)

type meshRPCFixture struct {
	walletrpc.UnimplementedWalletKitServer
	funded                         *walletrpc.FundPsbtRequest
	finalized, published, released int
	feeLimit                       int64
	psbt                           []byte
}

type meshLightningFixture struct {
	lnrpc.UnimplementedLightningServer
}
type meshRouterFixture struct {
	routerrpc.UnimplementedRouterServer
	fixture *meshRPCFixture
}

func (f *meshLightningFixture) GetInfo(context.Context, *lnrpc.GetInfoRequest) (*lnrpc.GetInfoResponse, error) {
	return &lnrpc.GetInfoResponse{SyncedToChain: true, Chains: []*lnrpc.Chain{{Chain: "bitcoin", Network: "mainnet"}}}, nil
}
func (f *meshRPCFixture) FundPsbt(_ context.Context, r *walletrpc.FundPsbtRequest) (*walletrpc.FundPsbtResponse, error) {
	f.funded = r
	return &walletrpc.FundPsbtResponse{FundedPsbt: f.psbt, ChangeOutputIndex: 1, LockedUtxos: []*walletrpc.UtxoLease{{Value: 10000, Id: make([]byte, 32), Outpoint: &lnrpc.OutPoint{TxidBytes: make([]byte, 32)}}}}, nil
}
func (f *meshRPCFixture) FinalizePsbt(_ context.Context, r *walletrpc.FinalizePsbtRequest) (*walletrpc.FinalizePsbtResponse, error) {
	f.finalized++
	return &walletrpc.FinalizePsbtResponse{RawFinalTx: []byte("signed-test-transaction")}, nil
}
func (f *meshRPCFixture) PublishTransaction(context.Context, *walletrpc.Transaction) (*walletrpc.PublishResponse, error) {
	f.published++
	return &walletrpc.PublishResponse{}, nil
}
func (f *meshRPCFixture) ReleaseOutput(context.Context, *walletrpc.ReleaseOutputRequest) (*walletrpc.ReleaseOutputResponse, error) {
	f.released++
	return &walletrpc.ReleaseOutputResponse{}, nil
}
func (f *meshRouterFixture) SendPaymentV2(r *routerrpc.SendPaymentRequest, s routerrpc.Router_SendPaymentV2Server) error {
	f.fixture.feeLimit = r.FeeLimitMsat
	return s.Send(&lnrpc.Payment{Status: lnrpc.Payment_SUCCEEDED})
}

func TestMeshWalletKitSignsWithoutPublishingAndHonorsZeroRoutingFee(t *testing.T) {
	fixture := &meshRPCFixture{}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(5000, []byte{0x51}))
	tx.AddTxOut(wire.NewTxOut(4000, []byte{0x51}))
	packet, err := psbt.NewFromUnsignedTx(tx)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err = packet.Serialize(&b); err != nil {
		t.Fatal(err)
	}
	fixture.psbt = b.Bytes()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	lnrpc.RegisterLightningServer(server, &meshLightningFixture{})
	walletrpc.RegisterWalletKitServer(server, fixture)
	routerrpc.RegisterRouterServer(server, &meshRouterFixture{fixture: fixture})
	go server.Serve(listener)
	defer server.Stop()
	conn, err := grpc.NewClient("passthrough:///memory-only", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	enabled := true
	client := &Client{cfg: &config.Config{LND: config.LNDConfig{SharedGRPC: &enabled}}, grpcConns: map[grpcConnRole]*grpc.ClientConn{grpcRoleAdminUnary: conn, grpcRoleAdminStream: conn}}
	funded, err := client.FundMeshTransaction(context.Background(), "1BoatSLRHtKNngkdXEeobR76b53LETtpyT", 5000, 2)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.funded.MinConfs != 1 || fixture.funded.GetSatPerVbyte() != 2 || funded.Preview.FeeSat != 1000 || funded.Preview.ChangeSat != 4000 || funded.Preview.TotalDebitSat != 6000 {
		t.Fatal("incorrect funding policy or preview")
	}
	if fixture.finalized != 0 || fixture.published != 0 {
		t.Fatal("preview signed or published")
	}
	if _, err = client.FinalizeMeshTransaction(context.Background(), funded); err != nil {
		t.Fatal(err)
	}
	if fixture.finalized != 1 || fixture.published != 0 {
		t.Fatal("signing performed a publication")
	}
	client.ReleaseMeshTransaction(context.Background(), funded)
	if fixture.released != 1 {
		t.Fatal("preview inputs not released")
	}
	for _, fee := range []int64{0, 13} {
		if err = client.PayMeshInvoice(context.Background(), "lnbc-test", fee); err != nil {
			t.Fatal(err)
		}
		if fixture.feeLimit != fee*1000 {
			t.Fatal("approved fee limit changed")
		}
	}
}
