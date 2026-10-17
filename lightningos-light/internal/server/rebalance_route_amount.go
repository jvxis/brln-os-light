package server

import (
	"context"
	"errors"
	"math/bits"
	"strings"

	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// One initial query plus at most four smaller queries, sharing the caller's
// existing attempt/round/job deadline. This is route discovery, not a payment
// retry: never use it after an ambiguous SendToRoute/SendPayment result.
const rebalanceRouteAmountMaxQueries = 5

type rebalanceRouteAmountResult struct {
	Routes       []*lnrpc.Route
	AmountSat    int64
	FeeLimitMsat int64
	// Probe is set when the route was found at the probe fee (the advertised
	// fee's cap) on the minimum lot, after the realized cap found nothing.
	Probe bool
}

// rebalanceRouteProbe lets the amount ladder end with one small lot priced on
// the advertised fee instead of the realized price cap.
//
// With the realized price cap (0.5.43) the fee limit of every attempt is
// econ × the price the channel actually sold at. When the market's route
// into the channel costs more than that, no amount finds a route — the
// ladder halves down to the execution minimum and every source fails — so
// the legacy loop stopped producing partials, and nothing measured what the
// route costs now. The probe keeps the realized cap for every rung but, when
// the minimum lot also fails, retries it once at the advertised fee's cap.
// The lot is the execution minimum (10k sats by default), so the most it
// can overpay is a few sats, and the budget per job is one normal lot
// (min_amount_sat). A probe that pays records the price in pair stats and
// the recent rebalance cost, which is how AutoFee and the ladder learn it.
type rebalanceRouteProbe struct {
	Policy    lndclient.ChannelPolicySnapshot
	BudgetSat int64
}

// realizedCapProbeBudgetSat: the probe only exists when the realized cap is
// on and actually lowered this target's reference below the advertised fee.
func realizedCapProbeBudgetSat(cfg RebalanceConfig, capped lndclient.ChannelPolicySnapshot, advertised lndclient.ChannelPolicySnapshot) int64 {
	if !cfg.RealizedPriceCapEnabled || cfg.FeeLimitPpm > 0 || advertised.FeeRatePpm <= capped.FeeRatePpm {
		return 0
	}
	budget := effectiveStartAmountSat(cfg)
	if minimum := effectiveMinExecuteSat(cfg); budget < minimum {
		budget = minimum
	}
	return budget
}

func isRebalanceAmountNoRoute(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	code := status.Code(err)
	if code != codes.Unknown && code != codes.NotFound {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(status.Convert(err).Message()))
	return message == "no route" || message == "no routes found" || message == "unable to find a path to destination"
}

// Keep both the current policy ceiling and the caller's fee-ladder/warm-route
// ppm ceiling. Reusing the original absolute fee budget for a smaller amount
// would silently make its allowed ppm more expensive. Integer arithmetic avoids
// rounding up and overflow even for large operator amounts.
func rebalanceResizedFeeLimit(amount, initialAmount, initialFee, policyFee int64) int64 {
	if amount <= 0 || initialAmount <= 0 || amount > initialAmount || initialFee <= 0 || policyFee <= 0 {
		return 0
	}
	hi, lo := bits.Mul64(uint64(initialFee), uint64(amount))
	scaled, _ := bits.Div64(hi, lo, uint64(initialAmount))
	if int64(scaled) < policyFee {
		return int64(scaled)
	}
	return policyFee
}

func queryRebalanceRoutesWithAmountFallback(
	ctx context.Context, amount, feeLimit int64, cfg RebalanceConfig,
	target lndclient.ChannelPolicySnapshot, source *lndclient.ChannelPolicySnapshot,
	query func(context.Context, int64, int64) ([]*lnrpc.Route, error),
	onReduce func(int64, int64),
) (rebalanceRouteAmountResult, error) {
	return queryRebalanceRoutesWithAmountFallbackAndProbe(ctx, amount, feeLimit, cfg, target, source, query, onReduce, nil)
}

func queryRebalanceRoutesWithAmountFallbackAndProbe(
	ctx context.Context, amount, feeLimit int64, cfg RebalanceConfig,
	target lndclient.ChannelPolicySnapshot, source *lndclient.ChannelPolicySnapshot,
	query func(context.Context, int64, int64) ([]*lnrpc.Route, error),
	onReduce func(int64, int64),
	probe *rebalanceRouteProbe,
) (rebalanceRouteAmountResult, error) {
	result := rebalanceRouteAmountResult{AmountSat: amount, FeeLimitMsat: feeLimit}
	minimum := effectiveMinExecuteSat(cfg)
	if minimum <= 0 {
		minimum = 1
	}
	if amount < minimum || feeLimit <= 0 {
		return result, errors.New("invalid route query amount or fee limit")
	}
	// tryProbe: the minimum lot found nothing at the realized cap; ask once
	// more at the advertised fee's cap, if that is actually higher and the
	// job still has probe budget.
	tryProbe := func(err error) (rebalanceRouteAmountResult, error) {
		if probe == nil || probe.BudgetSat < minimum || !cfg.AmountProbeAdaptive || !isRebalanceAmountNoRoute(err) {
			return result, err
		}
		probeFee, feeErr := calcFeeLimitMsat(minimum*1000, probe.Policy, source, cfg)
		if feeErr != nil || probeFee <= result.FeeLimitMsat {
			return result, err
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		routes, probeErr := query(ctx, minimum, probeFee)
		if probeErr == nil && len(routes) == 0 {
			probeErr = errors.New("no route")
		}
		if probeErr != nil {
			return result, err
		}
		return rebalanceRouteAmountResult{Routes: routes, AmountSat: minimum, FeeLimitMsat: probeFee, Probe: true}, nil
	}
	for n := 0; ; n++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		routes, err := query(ctx, result.AmountSat, result.FeeLimitMsat)
		if err == nil && len(routes) == 0 {
			err = errors.New("no route")
		}
		if err == nil {
			result.Routes = routes
			return result, nil
		}
		if result.AmountSat <= minimum {
			return tryProbe(err)
		}
		if !cfg.AmountProbeAdaptive || !isRebalanceAmountNoRoute(err) || n+1 >= rebalanceRouteAmountMaxQueries {
			return result, err
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		next := result.AmountSat / 2
		if next < minimum || n+2 == rebalanceRouteAmountMaxQueries {
			next = minimum
		}
		policyFee, feeErr := calcFeeLimitMsat(next*1000, target, source, cfg)
		if feeErr != nil {
			return result, feeErr
		}
		nextFee := rebalanceResizedFeeLimit(next, amount, feeLimit, policyFee)
		if nextFee <= 0 {
			return result, err
		}
		if onReduce != nil {
			onReduce(result.AmountSat, next)
		}
		result.AmountSat, result.FeeLimitMsat = next, nextFee
	}
}
