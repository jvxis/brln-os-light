package lndclient

import (
	"context"
	"google.golang.org/grpc"
	"testing"
	"time"

	"lightningos-light/lnrpc"
)

type recentAliasClient struct {
	lnrpc.LightningClient
	alias string
}

func (f recentAliasClient) GetInfo(context.Context, *lnrpc.GetInfoRequest, ...grpc.CallOption) (*lnrpc.GetInfoResponse, error) {
	return &lnrpc.GetInfoResponse{IdentityPubkey: "self"}, nil
}

func (f recentAliasClient) ListChannels(context.Context, *lnrpc.ListChannelsRequest, ...grpc.CallOption) (*lnrpc.ListChannelsResponse, error) {
	return &lnrpc.ListChannelsResponse{Channels: []*lnrpc.Channel{{ChanId: 9, ChannelPoint: "point", RemotePubkey: "02abcdef1234567890", PeerAlias: f.alias, Private: true}}}, nil
}

func TestLightningActivityAliasLookupFailureUsesPubkey(t *testing.T) {
	for _, tc := range []struct{ alias, want string }{
		{"Visible peer", "Visible peer"},
		{"unable to lookup peer alias: alias for node not found", "02abcdef1234"},
		{"  Unable to lookup peer alias: missing  ", "02abcdef1234"},
		{"", "02abcdef1234"},
	} {
		items, err := (&Client{}).buildLightningActivity(context.Background(), recentAliasClient{alias: tc.alias}, []*lnrpc.Invoice{{State: lnrpc.Invoice_SETTLED, Value: 123, CreationDate: 1, Htlcs: []*lnrpc.InvoiceHTLC{{ChanId: 9}}}}, nil)
		if err != nil || len(items) != 1 {
			t.Fatalf("activity: %v, %v", items, err)
		}
		if items[0].ChannelAlias != tc.want || items[0].ChannelPoint != "point" || items[0].AmountSat != 123 {
			t.Fatalf("alias=%q: unexpected activity: %+v", tc.alias, items[0])
		}
	}
}

func TestRecentPaymentTimestampPrefersCreationDate(t *testing.T) {
	pay := &lnrpc.Payment{
		CreationDate:   1_710_000_000,
		CreationTimeNs: 1_720_000_000_123_000_000,
	}

	got := recentPaymentTimestamp(pay)
	want := time.Unix(pay.CreationDate, 0).UTC()
	if !got.Equal(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestRecentPaymentTimestampFallsBackToCreationTimeNs(t *testing.T) {
	pay := &lnrpc.Payment{
		CreationTimeNs: 1_720_000_000_123_000_000,
	}

	got := recentPaymentTimestamp(pay)
	want := time.Unix(0, pay.CreationTimeNs).UTC()
	if !got.Equal(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestRecentPaymentPageSizes(t *testing.T) {
	got := recentPaymentPageSizes(1000)
	want := []uint64{1000, 500, 200, 100, 50}
	if len(got) != len(want) {
		t.Fatalf("expected %d sizes, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected size %d at position %d, got %d", want[i], i, got[i])
		}
	}
}

func TestRecentPaymentPageSizesDeduplicatesLimit(t *testing.T) {
	got := recentPaymentPageSizes(200)
	want := []uint64{200, 500, 100, 50}
	if len(got) != len(want) {
		t.Fatalf("expected %d sizes, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected size %d at position %d, got %d", want[i], i, got[i])
		}
	}
}
