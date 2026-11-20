package server

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"lightningos-light/internal/config"
	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"
	"lightningos-light/lnrpc/routerrpc"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type rebalanceProbeRPC struct {
	routerrpc.UnimplementedRouterServer
	route *lnrpc.Route
	mu    sync.Mutex
	codes []lnrpc.Failure_FailureCode
	calls int
}

func (f *rebalanceProbeRPC) BuildRoute(context.Context, *routerrpc.BuildRouteRequest) (*routerrpc.BuildRouteResponse, error) {
	return &routerrpc.BuildRouteResponse{Route: f.route}, nil
}

func (f *rebalanceProbeRPC) SendToRouteV2(context.Context, *routerrpc.SendToRouteRequest) (*lnrpc.HTLCAttempt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	code := f.codes[f.calls%len(f.codes)]
	f.calls++
	return &lnrpc.HTLCAttempt{Status: lnrpc.HTLCAttempt_FAILED, Failure: &lnrpc.Failure{Code: code, FailureSourceIndex: 1}}, nil
}

func TestRebalanceProbeFeedbackRoundTrip(t *testing.T) {
	a, b, c := "02"+strings.Repeat("1", 64), "02"+strings.Repeat("2", 64), "02"+strings.Repeat("3", 64)
	route := &lnrpc.Route{Hops: []*lnrpc.Hop{{ChanId: 11, PubKey: b}, {ChanId: 22, PubKey: c}, {ChanId: 33, PubKey: a}}}
	cert := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer cert.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: cert.TLS.Certificates, MinVersion: tls.VersionTLS12})))
	f := &rebalanceProbeRPC{route: route}
	routerrpc.RegisterRouterServer(g, f)
	go g.Serve(listener)
	t.Cleanup(g.Stop)
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.LND.GRPCHost = listener.Addr().String()
	cfg.LND.TLSCertPath = filepath.Join(dir, "tls.cert")
	cfg.LND.AdminMacaroonPath = filepath.Join(dir, "fixture.macaroon")
	if err := os.WriteFile(cfg.LND.TLSCertPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.LND.AdminMacaroonPath, []byte("fake"), 0600); err != nil {
		t.Fatal(err)
	}
	client := lndclient.New(cfg, log.New(io.Discard, "", 0))
	t.Cleanup(func() { client.Close() })
	s := &RebalanceService{lnd: client}

	for _, tc := range []struct {
		name        string
		codes       []lnrpc.Failure_FailureCode
		wantIgnored []uint64
		wantUsable  bool
		wantError   bool
	}{
		{"temporary failure skips reporting node's outgoing channel only", []lnrpc.Failure_FailureCode{lnrpc.Failure_TEMPORARY_CHANNEL_FAILURE}, []uint64{22}, false, false},
		{"temporary failure at a larger amount keeps a usable route", []lnrpc.Failure_FailureCode{lnrpc.Failure_INCORRECT_OR_UNKNOWN_PAYMENT_DETAILS, lnrpc.Failure_TEMPORARY_CHANNEL_FAILURE}, nil, true, false},
		{"unrelated failure does not blacklist any channel", []lnrpc.Failure_FailureCode{lnrpc.Failure_INVALID_ONION_PAYLOAD}, nil, false, true},
		{"unrelated terminal failure does not reuse earlier liquidity verdict", []lnrpc.Failure_FailureCode{lnrpc.Failure_TEMPORARY_CHANNEL_FAILURE, lnrpc.Failure_INVALID_ONION_PAYLOAD}, nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.mu.Lock()
			f.codes, f.calls = tc.codes, 0
			f.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var got []uint64
			amount, err := s.probeRouteWithFailure(ctx, route, 1000, 0, 2, 33, lndclient.ChannelPolicySnapshot{}, lndclient.ChannelPolicySnapshot{}, RebalanceConfig{FeeLimitPpm: 1000}, func(r *lnrpc.Route, index uint32) {
				if edge := rebalanceProbeFailedEdge(r, index, a); edge != nil {
					if edge.DirectionReverse {
						t.Fatal("reverse direction was excluded")
					}
					got = append(got, edge.ChannelId)
				}
			})
			if !reflect.DeepEqual(got, tc.wantIgnored) || (amount > 0) != tc.wantUsable || (err != nil) != tc.wantError {
				t.Fatalf("ignored=%v amount=%d err=%v", got, amount, err)
			}
		})
	}
	if edge := rebalanceProbeFailedEdge(route, 2, a); edge == nil || edge.ChannelId != 33 || !edge.DirectionReverse {
		t.Fatalf("expected return channel in its own direction, got %v", edge)
	}
	if edge := rebalanceProbeFailedEdge(route, 0, a); edge == nil || edge.ChannelId != 11 || edge.DirectionReverse {
		t.Fatalf("expected sender's outgoing channel, got %v", edge)
	}
	if edge := rebalanceProbeFailedEdge(route, 3, a); edge != nil {
		t.Fatalf("the destination has no onward hop, got %v", edge)
	}
}
