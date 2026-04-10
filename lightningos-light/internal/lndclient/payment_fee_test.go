package lndclient

import (
	"bytes"
	"context"
	"testing"
	"time"

	"lightningos-light/lnrpc"
)

func TestDefaultRouterPaymentFeeLimitMsatForDecodedInvoice(t *testing.T) {
	t.Parallel()

	const maxFeeLimitMsat = int64(^uint64(0) >> 1)

	tests := []struct {
		name    string
		decoded DecodedInvoice
		want    int64
	}{
		{
			name:    "unknown amount falls back to unlimited",
			decoded: DecodedInvoice{},
			want:    maxFeeLimitMsat,
		},
		{
			name: "small amounts keep 100 percent default",
			decoded: DecodedInvoice{
				AmountMsat: 900_000,
			},
			want: 900_000,
		},
		{
			name: "larger amounts use five percent default",
			decoded: DecodedInvoice{
				AmountMsat: 86_438_000,
			},
			want: 4_321_900,
		},
		{
			name: "sat amount is used when msat is missing",
			decoded: DecodedInvoice{
				AmountSat: 20_000,
			},
			want: 1_000_000,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := defaultRouterPaymentFeeLimitMsatForDecodedInvoice(tt.decoded)
			if got != tt.want {
				t.Fatalf("defaultRouterPaymentFeeLimitMsatForDecodedInvoice() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPaymentPreviewFeeHeadroomSat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		feeMsat int64
		want    int64
	}{
		{
			name:    "adds twenty five percent headroom",
			feeMsat: 251_000,
			want:    314,
		},
		{
			name:    "rounds millisats up",
			feeMsat: 1,
			want:    1,
		},
		{
			name:    "zero fee keeps explicit one sat cap",
			feeMsat: 0,
			want:    1,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := paymentPreviewFeeHeadroomSat(tt.feeMsat)
			if got != tt.want {
				t.Fatalf("paymentPreviewFeeHeadroomSat(%d) = %d, want %d", tt.feeMsat, got, tt.want)
			}
		})
	}
}

func TestRouteTotalFeeMsat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		route *lnrpc.Route
		want  int64
	}{
		{
			name:  "uses route millisat total",
			route: &lnrpc.Route{TotalFeesMsat: 12_345, TotalFees: 99},
			want:  12_345,
		},
		{
			name:  "falls back to route sat total",
			route: &lnrpc.Route{TotalFees: 7},
			want:  7_000,
		},
		{
			name: "falls back to hop totals",
			route: &lnrpc.Route{Hops: []*lnrpc.Hop{
				{FeeMsat: 2_500},
				{Fee: 3},
			}},
			want: 5_500,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := routeTotalFeeMsat(tt.route)
			if got != tt.want {
				t.Fatalf("routeTotalFeeMsat() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRouteForInvoicePaymentAddsMPPRecord(t *testing.T) {
	t.Parallel()

	paymentAddr := []byte{1, 2, 3}
	route := &lnrpc.Route{Hops: []*lnrpc.Hop{
		{ChanId: 10, PubKey: "first"},
		{ChanId: 20, PubKey: "destination"},
	}}

	got, err := routeForInvoicePayment(route, DecodedInvoice{PaymentAddr: paymentAddr}, 123_456)
	if err != nil {
		t.Fatalf("routeForInvoicePayment() error = %v", err)
	}
	if got == route {
		t.Fatalf("routeForInvoicePayment() returned the original route")
	}
	if route.Hops[1].MppRecord != nil {
		t.Fatalf("routeForInvoicePayment() mutated the original route")
	}
	if got.Hops[1].MppRecord == nil {
		t.Fatalf("routeForInvoicePayment() did not add an MPP record")
	}
	if got.Hops[1].MppRecord.TotalAmtMsat != 123_456 {
		t.Fatalf("MPP total amount = %d, want 123456", got.Hops[1].MppRecord.TotalAmtMsat)
	}
	if got.Hops[1].TotalAmtMsat != 123_456 {
		t.Fatalf("final hop total amount = %d, want 123456", got.Hops[1].TotalAmtMsat)
	}
	paymentAddr[0] = 9
	if !bytes.Equal(got.Hops[1].MppRecord.PaymentAddr, []byte{1, 2, 3}) {
		t.Fatalf("MPP payment addr = %v, want independent copy", got.Hops[1].MppRecord.PaymentAddr)
	}
}

func TestRouteAlternativeIgnoredEdgeSetsKeepsSelectedFirstHop(t *testing.T) {
	t.Parallel()

	route := &lnrpc.Route{Hops: []*lnrpc.Hop{
		{ChanId: 10, PubKey: "first"},
		{ChanId: 20, PubKey: "second"},
		{ChanId: 30, PubKey: "third"},
	}}

	got := routeAlternativeIgnoredEdgeSets(route, true)
	if len(got) != 3 {
		t.Fatalf("routeAlternativeIgnoredEdgeSets() returned %d sets, want 3", len(got))
	}
	if got[0][0].ChannelId != 20 || got[0][2].ChannelId != 30 {
		t.Fatalf("routeAlternativeIgnoredEdgeSets() full route set = channel ids %d, %d; want 20, 30", got[0][0].ChannelId, got[0][2].ChannelId)
	}
	if got[1][0].ChannelId != 20 || got[2][0].ChannelId != 30 {
		t.Fatalf("routeAlternativeIgnoredEdgeSets() single-edge sets = channel ids %d, %d; want 20, 30", got[1][0].ChannelId, got[2][0].ChannelId)
	}
}

func TestPaymentTimeoutSeconds(t *testing.T) {
	t.Parallel()

	t.Run("uses fallback when deadline has enough room", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if got := paymentTimeoutSeconds(ctx, 90); got != 90 {
			t.Fatalf("paymentTimeoutSeconds() = %d, want 90", got)
		}
	})

	t.Run("leaves response room before a short deadline", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		got := paymentTimeoutSeconds(ctx, 90)
		if got < 7 || got > 8 {
			t.Fatalf("paymentTimeoutSeconds() = %d, want 7 or 8", got)
		}
	})
}

func TestPaymentRouteProbeFromFailure(t *testing.T) {
	t.Parallel()

	route := &lnrpc.Route{Hops: []*lnrpc.Hop{
		{ChanId: 10, PubKey: "first"},
		{ChanId: 20, PubKey: "destination"},
	}}

	t.Run("final fake hash rejection means likely liquidity", func(t *testing.T) {
		t.Parallel()
		probe := paymentRouteProbeFromFailure(&lnrpc.Failure{
			Code:               lnrpc.Failure_INCORRECT_OR_UNKNOWN_PAYMENT_DETAILS,
			FailureSourceIndex: 2,
		}, route)
		if probe.Status != paymentRouteProbeStatusLikely || !probe.LikelyLiquid {
			t.Fatalf("paymentRouteProbeFromFailure() = status %q likely %v, want likely liquidity", probe.Status, probe.LikelyLiquid)
		}
	})

	t.Run("intermediate failure means failed liquidity probe", func(t *testing.T) {
		t.Parallel()
		probe := paymentRouteProbeFromFailure(&lnrpc.Failure{
			Code:               lnrpc.Failure_TEMPORARY_CHANNEL_FAILURE,
			FailureSourceIndex: 1,
		}, route)
		if probe.Status != paymentRouteProbeStatusFailed || probe.LikelyLiquid {
			t.Fatalf("paymentRouteProbeFromFailure() = status %q likely %v, want failed probe", probe.Status, probe.LikelyLiquid)
		}
		if probe.FailureHopIndex != 1 {
			t.Fatalf("FailureHopIndex = %d, want 1", probe.FailureHopIndex)
		}
	})
}

func TestSelectPreviewPaymentRoutesIncludesValidatedRoute(t *testing.T) {
	t.Parallel()

	probed := make([]probedPaymentRoute, 0, 6)
	for i := 0; i < 6; i++ {
		probe := PaymentRouteProbe{Status: paymentRouteProbeStatusFailed}
		if i == 5 {
			probe = PaymentRouteProbe{Status: paymentRouteProbeStatusLikely, LikelyLiquid: true}
		}
		probed = append(probed, probedPaymentRoute{
			route: &lnrpc.Route{
				TotalFeesMsat: int64(i+1) * 1000,
				Hops: []*lnrpc.Hop{
					{ChanId: uint64(i + 1)},
				},
			},
			probe: probe,
		})
	}

	selected := selectPreviewPaymentRoutes(probed, 5)
	if len(selected) != 5 {
		t.Fatalf("selectPreviewPaymentRoutes() returned %d routes, want 5", len(selected))
	}
	hasLikely := false
	for _, route := range selected {
		if route.probe.LikelyLiquid {
			hasLikely = true
			break
		}
	}
	if !hasLikely {
		t.Fatalf("selectPreviewPaymentRoutes() did not include the validated route")
	}
}

func TestSelectPreviewPaymentRoutesSpreadsFeesWithoutValidatedRoute(t *testing.T) {
	t.Parallel()

	probed := make([]probedPaymentRoute, 0, 10)
	for i := 0; i < 10; i++ {
		probed = append(probed, probedPaymentRoute{
			route: &lnrpc.Route{
				TotalFeesMsat: int64(i+1) * 1000,
				Hops: []*lnrpc.Hop{
					{ChanId: uint64(i + 1)},
				},
			},
			probe: PaymentRouteProbe{Status: paymentRouteProbeStatusFailed},
		})
	}

	selected := selectPreviewPaymentRoutes(probed, 5)
	if len(selected) != 5 {
		t.Fatalf("selectPreviewPaymentRoutes() returned %d routes, want 5", len(selected))
	}
	if got := routeTotalFeeMsat(selected[len(selected)-1].route); got != 10_000 {
		t.Fatalf("last selected fee = %d, want 10000 to include expensive graph candidates", got)
	}
	if got := routeTotalFeeMsat(selected[0].route); got != 1_000 {
		t.Fatalf("first selected fee = %d, want 1000", got)
	}
}

func TestSelectProbeCandidateRoutesKeepsFirstHopDiversity(t *testing.T) {
	t.Parallel()

	routes := []*lnrpc.Route{
		{TotalFeesMsat: 1_000, Hops: []*lnrpc.Hop{{ChanId: 1}, {ChanId: 11}}},
		{TotalFeesMsat: 2_000, Hops: []*lnrpc.Hop{{ChanId: 1}, {ChanId: 12}}},
		{TotalFeesMsat: 3_000, Hops: []*lnrpc.Hop{{ChanId: 1}, {ChanId: 13}}},
		{TotalFeesMsat: 4_000, Hops: []*lnrpc.Hop{{ChanId: 2}, {ChanId: 21}}},
		{TotalFeesMsat: 5_000, Hops: []*lnrpc.Hop{{ChanId: 3}, {ChanId: 31}}},
	}

	selected := selectProbeCandidateRoutes(routes, 4, 2)
	if len(selected) != 4 {
		t.Fatalf("selectProbeCandidateRoutes() returned %d routes, want 4", len(selected))
	}
	firstHops := make(map[string]struct{})
	for _, route := range selected {
		firstHops[routeFirstHopKey(route)] = struct{}{}
	}
	if _, ok := firstHops["2"]; !ok {
		t.Fatalf("selectProbeCandidateRoutes() did not include first hop 2")
	}
	if _, ok := firstHops["3"]; !ok {
		t.Fatalf("selectProbeCandidateRoutes() did not include first hop 3")
	}
}

func TestSelectProbeCandidateRoutesKeepsExpensiveFeeSpread(t *testing.T) {
	t.Parallel()

	routes := make([]*lnrpc.Route, 0, 10)
	for i := 0; i < 10; i++ {
		routes = append(routes, &lnrpc.Route{
			TotalFeesMsat: int64(i+1) * 1000,
			Hops: []*lnrpc.Hop{
				{ChanId: 1},
				{ChanId: uint64(i + 10)},
			},
		})
	}

	selected := selectProbeCandidateRoutes(routes, 5, 3)
	if len(selected) != 5 {
		t.Fatalf("selectProbeCandidateRoutes() returned %d routes, want 5", len(selected))
	}
	hasExpensive := false
	for _, route := range selected {
		if routeTotalFeeMsat(route) == 10_000 {
			hasExpensive = true
			break
		}
	}
	if !hasExpensive {
		t.Fatalf("selectProbeCandidateRoutes() did not include expensive fee-spread route")
	}
}

func TestPaymentRoutePreviewLimitsExpandForManyOutgoingChannels(t *testing.T) {
	t.Parallel()

	if got := paymentRoutePreviewCandidateLimit(5, 22); got <= 100 {
		t.Fatalf("paymentRoutePreviewCandidateLimit(5, 22) = %d, want above old 100 route cap", got)
	}
	if got := paymentRoutePreviewQueryLimit(5, 22); got <= paymentRoutePreviewBaseQueryCount {
		t.Fatalf("paymentRoutePreviewQueryLimit(5, 22) = %d, want above base query cap", got)
	}
	if got := paymentRoutePreviewProbeLimit(500, 5, 22); got != paymentRoutePreviewMaxProbes {
		t.Fatalf("paymentRoutePreviewProbeLimit(500, 5, 22) = %d, want max probe cap %d", got, paymentRoutePreviewMaxProbes)
	}
}

func TestPaymentRouteTokenRoundTripAndValidation(t *testing.T) {
	t.Parallel()

	route := &lnrpc.Route{
		TotalAmtMsat:  1_000_250_000,
		TotalFeesMsat: 250_000,
		Hops: []*lnrpc.Hop{
			{ChanId: 1, PubKey: "source-peer", FeeMsat: 250_000, AmtToForwardMsat: 1_000_000_000},
			{ChanId: 2, PubKey: "destination", AmtToForwardMsat: 1_000_000_000},
		},
	}

	token := encodePaymentRouteToken(route)
	if token == "" {
		t.Fatalf("encodePaymentRouteToken() returned empty token")
	}
	decoded, err := decodePaymentRouteToken(token)
	if err != nil {
		t.Fatalf("decodePaymentRouteToken() error = %v", err)
	}
	if routeKey(decoded) != routeKey(route) {
		t.Fatalf("decoded route key = %q, want %q", routeKey(decoded), routeKey(route))
	}
	if err := validatePaymentRouteForInvoice(decoded, DecodedInvoice{Destination: "destination"}, 1_000_000_000, 251); err != nil {
		t.Fatalf("validatePaymentRouteForInvoice() error = %v", err)
	}
	if err := validatePaymentRouteForInvoice(decoded, DecodedInvoice{Destination: "other"}, 1_000_000_000, 251); err == nil {
		t.Fatalf("validatePaymentRouteForInvoice() accepted destination mismatch")
	}
	if err := validatePaymentRouteForInvoice(decoded, DecodedInvoice{Destination: "destination"}, 1_000_000_000, 249); err == nil {
		t.Fatalf("validatePaymentRouteForInvoice() accepted fee above max")
	}
}

func TestMPPShardSizeCandidatesIncludesSmallShards(t *testing.T) {
	t.Parallel()

	got := mppShardSizeCandidates(1_000_000_000, 20)
	wants := map[int64]bool{
		250_000_000: false,
		100_000_000: false,
		50_000_000:  false,
	}
	for _, shard := range got {
		if _, ok := wants[shard]; ok {
			wants[shard] = true
		}
	}
	for shard, found := range wants {
		if !found {
			t.Fatalf("mppShardSizeCandidates() = %v, missing %d", got, shard)
		}
	}
}

func TestSelectMPPLikelyRoutesCoversAmount(t *testing.T) {
	t.Parallel()

	probed := []probedPaymentRoute{
		{
			route: &lnrpc.Route{TotalFeesMsat: 5_000, Hops: []*lnrpc.Hop{
				{ChanId: 1, FeeMsat: 5_000},
				{ChanId: 11, AmtToForwardMsat: 100_000_000},
			}},
			probe: PaymentRouteProbe{Status: paymentRouteProbeStatusLikely, LikelyLiquid: true},
		},
		{
			route: &lnrpc.Route{TotalFeesMsat: 2_000, Hops: []*lnrpc.Hop{
				{ChanId: 2, FeeMsat: 2_000},
				{ChanId: 12, AmtToForwardMsat: 100_000_000},
			}},
			probe: PaymentRouteProbe{Status: paymentRouteProbeStatusLikely, LikelyLiquid: true},
		},
		{
			route: &lnrpc.Route{TotalFeesMsat: 1_000, Hops: []*lnrpc.Hop{
				{ChanId: 3, FeeMsat: 1_000},
				{ChanId: 13, AmtToForwardMsat: 100_000_000},
			}},
			probe: PaymentRouteProbe{Status: paymentRouteProbeStatusFailed},
		},
	}

	selected, covered := selectMPPLikelyRoutes(probed, 200_000_000, 3)
	if len(selected) != 2 {
		t.Fatalf("selectMPPLikelyRoutes() selected %d routes, want 2", len(selected))
	}
	if covered != 200_000_000 {
		t.Fatalf("selectMPPLikelyRoutes() covered %d msat, want 200000000", covered)
	}
	if got := routeTotalFeeMsat(selected[0].route); got != 2_000 {
		t.Fatalf("first selected fee = %d, want cheapest likely route first", got)
	}
}
