package server

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"
)

func TestRebalanceAmountNoRouteClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{errors.New("no route"), true},
		{status.Error(codes.Unknown, "unable to find a path to destination"), true},
		{status.Error(codes.NotFound, "no routes found"), true},
		{nil, false}, {context.Canceled, false}, {context.DeadlineExceeded, false},
		{status.Error(codes.Unavailable, "no route"), false},
		{status.Error(codes.PermissionDenied, "no route"), false},
		{status.Error(codes.DeadlineExceeded, "no route"), false},
		{errors.New("route target channel mismatch"), false},
		{errors.New("channel disabled"), false}, {errors.New("unknown next peer"), false},
		{errors.New("payment failed: FAILURE_REASON_NO_ROUTE"), false},
	} {
		if got := isRebalanceAmountNoRoute(tc.err); got != tc.want {
			t.Fatalf("%v: got %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestQueryRebalanceRoutesAmountFallback(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		amount, minimum, successAt int64
		split, adaptive            bool
		want                       []int64
	}{
		{"same source 100k to 50k", 100_000, 10_000, 50_000, true, true, []int64{100_000, 50_000}},
		{"50k to execution floor", 50_000, 10_000, 10_000, true, true, []int64{50_000, 25_000, 12_500, 10_000}},
		{"floor respected without split", 100_000, 50_000, 50_000, false, true, []int64{100_000, 50_000}},
		{"bounded even for huge amount", 10_000_000, 10_000, 0, true, true, []int64{10_000_000, 5_000_000, 2_500_000, 1_250_000, 10_000}},
		{"floor once", 10_000, 10_000, 0, true, true, []int64{10_000}},
		{"operator disables adaptive", 100_000, 10_000, 0, true, false, []int64{100_000}},
		{"first success unchanged", 100_000, 10_000, 100_000, true, true, []int64{100_000}},
		{"below floor never queried", 9_000, 10_000, 0, true, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultRebalanceConfig()
			cfg.MinSplitEnabled, cfg.AmountProbeAdaptive = tc.split, tc.adaptive
			cfg.MinAmountSat, cfg.MinExecuteSat = tc.minimum, tc.minimum
			cfg.FeeLimitPpm = 1000
			var amounts, fees []int64
			reductions := 0
			route := &lnrpc.Route{Hops: []*lnrpc.Hop{{ChanId: 123}}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			initialFee := tc.amount / 2 // 500 ppm: below the 1000 ppm policy cap.
			res, err := queryRebalanceRoutesWithAmountFallback(ctx, tc.amount, initialFee, cfg, lndclient.ChannelPolicySnapshot{}, nil,
				func(queryCtx context.Context, amount, fee int64) ([]*lnrpc.Route, error) {
					if queryCtx != ctx {
						t.Fatal("attempt context/deadline was replaced")
					}
					amounts, fees = append(amounts, amount), append(fees, fee)
					if amount == tc.successAt {
						return []*lnrpc.Route{route}, nil
					}
					return nil, status.Error(codes.Unknown, "unable to find a path to destination")
				}, func(from, to int64) {
					if to >= from || to < tc.minimum {
						t.Fatalf("invalid reduction %d -> %d", from, to)
					}
					reductions++
				})
			if !reflect.DeepEqual(amounts, tc.want) {
				t.Fatalf("queries %v, want %v", amounts, tc.want)
			}
			if reductions != max(0, len(amounts)-1) {
				t.Fatalf("reduction telemetry %d", reductions)
			}
			for i, amount := range amounts {
				if fees[i] != amount/2 {
					t.Fatalf("fee rung changed: %d sat -> %d msat", amount, fees[i])
				}
			}
			if tc.successAt > 0 {
				if err != nil || res.AmountSat != tc.successAt || len(res.Routes) != 1 || res.Routes[0] != route {
					t.Fatalf("bad success: %+v %v", res, err)
				}
			} else if err == nil {
				t.Fatal("expected failure")
			}
			if len(amounts) > 0 && res.AmountSat != amounts[len(amounts)-1] {
				t.Fatal("failure reported original rather than last queried amount")
			}
		})
	}
}

func TestQueryRebalanceRoutesFallbackStops(t *testing.T) {
	for _, mode := range []string{"cancelled before", "cancelled during", "timeout", "permission", "wrong target", "fee zero", "empty routes"} {
		t.Run(mode, func(t *testing.T) {
			cfg := defaultRebalanceConfig()
			cfg.MinSplitEnabled = true
			cfg.MinExecuteSat = 10_000
			cfg.FeeLimitPpm = 1000
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled before" {
				cancel()
			}
			calls := 0
			fee := int64(50_000)
			if mode == "fee zero" {
				fee = 1
			}
			res, err := queryRebalanceRoutesWithAmountFallback(ctx, 100_000, fee, cfg, lndclient.ChannelPolicySnapshot{}, nil,
				func(context.Context, int64, int64) ([]*lnrpc.Route, error) {
					calls++
					switch mode {
					case "cancelled during":
						cancel()
					case "timeout":
						return nil, status.Error(codes.DeadlineExceeded, "timeout")
					case "permission":
						return nil, status.Error(codes.PermissionDenied, "denied")
					case "wrong target":
						return []*lnrpc.Route{{Hops: []*lnrpc.Hop{{ChanId: 999}}}}, nil
					case "empty routes":
						return nil, nil
					}
					return nil, errors.New("no route")
				}, nil)
			wantCalls := 1
			if mode == "cancelled before" {
				wantCalls = 0
			}
			if mode == "empty routes" {
				wantCalls = 5
			}
			if calls != wantCalls {
				t.Fatalf("calls=%d want=%d", calls, wantCalls)
			}
			if mode == "wrong target" {
				// Do not reinterpret a candidate on a sibling as no-route. The
				// caller's existing exact-target validation remains mandatory.
				if err != nil || res.Routes[0].Hops[0].ChanId != 999 {
					t.Fatal("candidate altered")
				}
			} else if err == nil {
				t.Fatal("missing failure")
			}
		})
	}
}

func TestRebalanceResizedFeeLimit(t *testing.T) {
	for _, tc := range []struct{ amount, initial, fee, policy, want int64 }{
		{50_000, 100_000, 50_000, 90_000, 25_000}, // preserve lower fee rung
		{50_000, 100_000, 50_000, 10_000, 10_000}, // policy still wins
		{10_000, 100_000, 1, 1000, 0},             // never round a zero cap up
		{3, 5, 9, 100, 5},
		{math.MaxInt64 / 2, math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64 / 2},
		{101, 100, 50, 50, 0}, {0, 100, 50, 50, 0},
	} {
		if got := rebalanceResizedFeeLimit(tc.amount, tc.initial, tc.fee, tc.policy); got != tc.want {
			t.Fatalf("%+v got=%d", tc, got)
		}
	}
}
