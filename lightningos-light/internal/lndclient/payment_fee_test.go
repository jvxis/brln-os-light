package lndclient

import (
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

func TestRouteAlternativeIgnoredEdgeSetsKeepsSelectedFirstHop(t *testing.T) {
	t.Parallel()

	route := &lnrpc.Route{Hops: []*lnrpc.Hop{
		{ChanId: 10, PubKey: "first"},
		{ChanId: 20, PubKey: "second"},
		{ChanId: 30, PubKey: "third"},
	}}

	got := routeAlternativeIgnoredEdgeSets(route, true)
	if len(got) != 2 {
		t.Fatalf("routeAlternativeIgnoredEdgeSets() returned %d sets, want 2", len(got))
	}
	if got[0][0].ChannelId != 20 || got[1][0].ChannelId != 30 {
		t.Fatalf("routeAlternativeIgnoredEdgeSets() = channel ids %d, %d; want 20, 30", got[0][0].ChannelId, got[1][0].ChannelId)
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
