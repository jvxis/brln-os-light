package lndclient

import "testing"

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
