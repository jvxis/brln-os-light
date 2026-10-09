package lndclient

import (
	"context"
	"encoding/json"
	"math"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"lightningos-light/internal/config"
	"lightningos-light/lnrpc"
)

type channelHTLCBoundsRPCFixture struct {
	lnrpc.UnimplementedLightningServer
}

func (*channelHTLCBoundsRPCFixture) ListChannels(context.Context, *lnrpc.ListChannelsRequest) (*lnrpc.ListChannelsResponse, error) {
	return &lnrpc.ListChannelsResponse{Channels: []*lnrpc.Channel{
		{ChanId: 1, ChannelPoint: "bounded", RemotePubkey: "peer", Capacity: 1_000_000, LocalBalance: 250_000,
			LocalConstraints:  &lnrpc.ChannelConstraints{MinHtlcMsat: 3001, MaxPendingAmtMsat: 100_000_123, ChanReserveSat: 50_000},
			RemoteConstraints: &lnrpc.ChannelConstraints{MinHtlcMsat: 1, MaxPendingAmtMsat: 950_000_000}},
		{ChanId: 2, ChannelPoint: "unlimited", LocalConstraints: &lnrpc.ChannelConstraints{MinHtlcMsat: 100_000, MaxPendingAmtMsat: math.MaxUint64}},
		{ChanId: 3, ChannelPoint: "missing", RemoteConstraints: &lnrpc.ChannelConstraints{MinHtlcMsat: 99, MaxPendingAmtMsat: 123}},
		{ChanId: 4, ChannelPoint: "zero", LocalConstraints: &lnrpc.ChannelConstraints{}},
	}}, nil
}

func (*channelHTLCBoundsRPCFixture) GetChanInfo(context.Context, *lnrpc.ChanInfoRequest) (*lnrpc.ChannelEdge, error) {
	return &lnrpc.ChannelEdge{Node1Pub: "local", Node2Pub: "peer", Node1Policy: &lnrpc.RoutingPolicy{FeeBaseMsat: 123, FeeRateMilliMsat: 456}}, nil
}

func TestListChannelsPreservesNegotiatedLocalHTLCBounds(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	lnrpc.RegisterLightningServer(server, &channelHTLCBoundsRPCFixture{})
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///htlc-bounds", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	enabled := true
	c := &Client{cfg: &config.Config{LND: config.LNDConfig{SharedGRPC: &enabled}}, grpcConns: map[grpcConnRole]*grpc.ClientConn{grpcRoleAdminUnary: conn}}
	channels, err := c.ListChannels(context.Background())
	if err != nil || len(channels) != 4 {
		t.Fatalf("ListChannels len=%d err=%v", len(channels), err)
	}
	if b := channels[0].LocalHTLCBounds; b == nil || b.MinMsat != 3001 || b.MaxMsat != 100_000_123 {
		t.Fatalf("local limits lost or replaced by remote limits: %+v", b)
	}
	if b := channels[1].LocalHTLCBounds; b == nil || b.MaxMsat != math.MaxUint64 || b.MinMsat != 100_000 {
		t.Fatalf("unlimited uint64 maximum corrupted: %+v", b)
	}
	if channels[2].LocalHTLCBounds != nil {
		t.Fatal("missing local constraints must remain unknown, not use remote constraints")
	}
	if b := channels[3].LocalHTLCBounds; b == nil || b.MinMsat != 0 || b.MaxMsat != 0 {
		t.Fatalf("explicit zero constraints confused with missing constraints: %+v", b)
	}
	ch := channels[0]
	if ch.CapacitySat != 1_000_000 || ch.LocalBalanceSat != 250_000 || ch.LocalChanReserveSat != 50_000 || ch.BaseFeeMsat == nil || *ch.BaseFeeMsat != 123 || ch.FeeRatePpm == nil || *ch.FeeRatePpm != 456 {
		t.Fatalf("existing channel mapping regressed: %+v", ch)
	}
	data, err := json.Marshal(ch)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "MinMsat") || strings.Contains(string(data), "LocalHTLCBounds") || strings.Contains(string(data), "MaxMsat") {
		t.Fatal("internal limits leaked into the public JSON contract")
	}
}
