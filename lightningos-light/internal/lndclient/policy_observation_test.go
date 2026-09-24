package lndclient

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"lightningos-light/internal/config"
	"lightningos-light/lnrpc"
)

func TestLocalPolicyObservationPreservesZeroAndUnknown(t *testing.T) {
	channels := []*lnrpc.Channel{{ChanId: 9007199254740993, ChannelPoint: "zero", Active: true}, {ChanId: 2, ChannelPoint: "missing"}, {ChanId: 3, ChannelPoint: "wrong-id"}}
	fees := []*lnrpc.ChannelFeeReport{{ChanId: channels[0].ChanId, ChannelPoint: "zero", InboundFeePerMil: -25}, {ChanId: 4, ChannelPoint: "wrong-id"}}
	got := localPolicyObservations(channels, fees)
	if len(got) != 3 || got[0].Fees == nil || got[0].Fees.RatePPM != 0 || got[0].Fees.InboundRatePPM != -25 || got[0].ChannelID != channels[0].ChanId {
		t.Fatalf("lost zero fee, signed inbound or precise ID: %+v", got)
	}
	if got[1].Fees != nil || got[2].Fees != nil {
		t.Fatal("unknown policy converted into zero")
	}
}

func TestPolicyApplicationObservationIsBoundedAndDistinguishesAcknowledgement(t *testing.T) {
	c := &Client{}
	stream := c.PolicyApplicationObservations()
	now := time.Now().UTC()
	minimum := uint64(1000)
	params := UpdateChannelPolicyParams{ChannelPoint: "test", MinHtlcMsat: &minimum}
	ctx := WithPolicyObservationSource(context.Background(), "manual")
	c.observePolicyApplication(ctx, params, now, &lnrpc.PolicyUpdateResponse{}, nil)
	minimum = 2000
	first := <-stream
	if !first.Acknowledged || first.Source != "manual" || *first.Request.MinHtlcMsat != 1000 {
		t.Fatalf("bad observation: %+v", first)
	}
	c.observePolicyApplication(ctx, params, now, &lnrpc.PolicyUpdateResponse{FailedUpdates: []*lnrpc.FailedUpdate{{}}}, nil)
	if (<-stream).Acknowledged {
		t.Fatal("partial failure treated as application")
	}
	c.observePolicyApplication(ctx, params, now, nil, errors.New("private error"))
	if (<-stream).Acknowledged {
		t.Fatal("RPC failure treated as application")
	}
	for i := 0; i < 257; i++ {
		c.observePolicyApplication(ctx, params, now, &lnrpc.PolicyUpdateResponse{}, nil)
	}
	if c.PolicyObservationLoss() != 1 || len(stream) != 256 {
		t.Fatal("queue must not block and must report lost observation")
	}
}

type policyObservationRPCFixture struct {
	lnrpc.UnimplementedLightningServer
	requests      []*lnrpc.PolicyUpdateRequest
	readOnlyCalls int
}

func (f *policyObservationRPCFixture) UpdateChannelPolicy(_ context.Context, req *lnrpc.PolicyUpdateRequest) (*lnrpc.PolicyUpdateResponse, error) {
	f.requests = append(f.requests, req)
	return &lnrpc.PolicyUpdateResponse{FailedUpdates: []*lnrpc.FailedUpdate{{}}}, nil
}
func (f *policyObservationRPCFixture) ListChannels(context.Context, *lnrpc.ListChannelsRequest) (*lnrpc.ListChannelsResponse, error) {
	f.readOnlyCalls++
	return &lnrpc.ListChannelsResponse{Channels: []*lnrpc.Channel{{ChanId: 1, ChannelPoint: "sample"}}}, nil
}
func (f *policyObservationRPCFixture) FeeReport(context.Context, *lnrpc.FeeReportRequest) (*lnrpc.FeeReportResponse, error) {
	f.readOnlyCalls++
	return &lnrpc.FeeReportResponse{ChannelFees: []*lnrpc.ChannelFeeReport{{ChanId: 1, ChannelPoint: "sample", FeePerMil: 25}}}, nil
}

func TestPolicyObservationDoesNotChangeRPCRequestOrResult(t *testing.T) {
	f := &policyObservationRPCFixture{}
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	lnrpc.RegisterLightningServer(server, f)
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///policy-observation", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	enabled := true
	c := &Client{cfg: &config.Config{LND: config.LNDConfig{SharedGRPC: &enabled}}, grpcConns: map[grpcConnRole]*grpc.ClientConn{grpcRoleAdminUnary: conn}}
	params := UpdateChannelPolicyParams{ChannelPoint: strings.Repeat("0", 64) + ":0", BaseFeeMsat: 123, FeeRatePpm: 400, TimeLockDelta: 144, InboundEnabled: true, InboundFeeRatePpm: -20}
	if err := c.UpdateChannelPolicy(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	stream := c.PolicyApplicationObservations()
	if err := c.UpdateChannelPolicy(WithPolicyObservationSource(context.Background(), "autofee"), params); err != nil {
		t.Fatal("instrumentation changed existing RPC return", err)
	}
	if !reflect.DeepEqual(f.requests[0], f.requests[1]) {
		t.Fatal("instrumentation changed policy request")
	}
	if (<-stream).Acknowledged {
		t.Fatal("FailedUpdates must remain visible in diagnostics")
	}
	observed, err := c.ObserveLocalPolicies(context.Background())
	if err != nil || len(observed) != 1 || observed[0].Fees.RatePPM != 25 {
		t.Fatalf("observation: %v %v", observed, err)
	}
	if len(f.requests) != 2 || f.readOnlyCalls != 2 {
		t.Fatal("sampler changed a policy or made per-channel RPCs")
	}
}
