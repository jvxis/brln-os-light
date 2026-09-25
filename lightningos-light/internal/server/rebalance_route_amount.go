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
	result := rebalanceRouteAmountResult{AmountSat: amount, FeeLimitMsat: feeLimit}
	minimum := effectiveMinExecuteSat(cfg)
	if minimum <= 0 {
		minimum = 1
	}
	if amount < minimum || feeLimit <= 0 {
		return result, errors.New("invalid route query amount or fee limit")
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
		if !cfg.AmountProbeAdaptive || !isRebalanceAmountNoRoute(err) || result.AmountSat <= minimum || n+1 >= rebalanceRouteAmountMaxQueries {
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
