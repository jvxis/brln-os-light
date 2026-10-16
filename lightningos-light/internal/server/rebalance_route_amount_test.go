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

// The realized price cap left the amount ladder with no route at any rung for
// targets whose market route costs more than econ × the realized sale price,
// so the legacy loop stopped producing partials. The probe retries the
// minimum lot once at the advertised fee's cap.
func TestQueryRebalanceRoutesProbeLotAtMinimum(t *testing.T) {
	cfg := defaultRebalanceConfig()
	cfg.MinSplitEnabled, cfg.AmountProbeAdaptive = true, true
	cfg.MinAmountSat, cfg.MinExecuteSat = 10_000, 10_000
	cfg.RealizedPriceCapEnabled, cfg.EconRatio = true, 0.8
	// RA⚡KO, 2026-10-16: realized 343 ppm → cap 274; advertised 381 → 305;
	// the market route costs 300 ppm, so only the probe finds it.
	capped := lndclient.ChannelPolicySnapshot{FeeRatePpm: 343}
	advertised := lndclient.ChannelPolicySnapshot{FeeRatePpm: 381}
	route := &lnrpc.Route{Hops: []*lnrpc.Hop{{ChanId: 7}}}
	run := func(probe *rebalanceRouteProbe, adaptive bool) (rebalanceRouteAmountResult, error, []int64, []int64) {
		cfg := cfg
		cfg.AmountProbeAdaptive = adaptive
		var amounts, fees []int64
		initialFee, _ := calcFeeLimitMsat(100_000*1000, capped, nil, cfg)
		res, err := queryRebalanceRoutesWithAmountFallbackAndProbe(context.Background(), 100_000, initialFee, cfg, capped, nil,
			func(_ context.Context, amount, fee int64) ([]*lnrpc.Route, error) {
				amounts, fees = append(amounts, amount), append(fees, fee)
				if feeMsatToPpm(fee, amount) >= 300 {
					return []*lnrpc.Route{route}, nil
				}
				return nil, status.Error(codes.Unknown, "unable to find a path to destination")
			}, nil, probe)
		return res, err, amounts, fees
	}

	res, err, amounts, fees := run(&rebalanceRouteProbe{Policy: advertised, BudgetSat: 100_000}, true)
	if err != nil || !res.Probe || res.AmountSat != 10_000 || len(res.Routes) != 1 {
		t.Fatalf("probe lot expected at the minimum: res=%+v err=%v", res, err)
	}
	if !reflect.DeepEqual(amounts, []int64{100_000, 50_000, 25_000, 12_500, 10_000, 10_000}) {
		t.Fatalf("ladder then one probe query, got %v", amounts)
	}
	if got := feeMsatToPpm(fees[len(fees)-1], 10_000); got != 304 && got != 305 {
		t.Fatalf("probe priced on the advertised fee × econ (~305 ppm), got %d", got)
	}
	if got := feeMsatToPpm(fees[len(fees)-2], 10_000); got > 275 {
		t.Fatalf("the realized rung before the probe must stay at the realized cap, got %d", got)
	}

	if res, err, amounts, _ := run(nil, true); err == nil || res.Probe || len(amounts) != 5 {
		t.Fatalf("no probe object, no probe query: res=%+v err=%v amounts=%v", res, err, amounts)
	}
	if res, err, amounts, _ := run(&rebalanceRouteProbe{Policy: advertised, BudgetSat: 9_000}, true); err == nil || res.Probe || len(amounts) != 5 {
		t.Fatalf("budget below the minimum lot must not probe: %+v %v %v", res, err, amounts)
	}
	if res, err, amounts, _ := run(&rebalanceRouteProbe{Policy: capped, BudgetSat: 100_000}, true); err == nil || res.Probe || len(amounts) != 5 {
		t.Fatalf("a probe fee no higher than the realized cap is pointless: %+v %v %v", res, err, amounts)
	}
	if res, err, amounts, _ := run(&rebalanceRouteProbe{Policy: advertised, BudgetSat: 100_000}, false); err == nil || res.Probe || len(amounts) != 1 {
		t.Fatalf("operator disabled adaptive amounts: no ladder, no probe: %+v %v %v", res, err, amounts)
	}
}

func TestRealizedCapProbeBudget(t *testing.T) {
	cfg := defaultRebalanceConfig()
	cfg.MinAmountSat, cfg.MinExecuteSat = 100_000, 10_000
	capped := lndclient.ChannelPolicySnapshot{FeeRatePpm: 343}
	advertised := lndclient.ChannelPolicySnapshot{FeeRatePpm: 381}
	if got := realizedCapProbeBudgetSat(cfg, capped, advertised); got != 0 {
		t.Fatalf("cap off: no probe, got %d", got)
	}
	cfg.RealizedPriceCapEnabled = true
	if got := realizedCapProbeBudgetSat(cfg, capped, advertised); got != 100_000 {
		t.Fatalf("one normal lot of probe budget per job, got %d", got)
	}
	if got := realizedCapProbeBudgetSat(cfg, advertised, advertised); got != 0 {
		t.Fatalf("cap did not lower the reference: no probe, got %d", got)
	}
	cfg.FeeLimitPpm = 500
	if got := realizedCapProbeBudgetSat(cfg, capped, advertised); got != 0 {
		t.Fatalf("a fixed fee limit has no advertised cap to probe with, got %d", got)
	}
}
