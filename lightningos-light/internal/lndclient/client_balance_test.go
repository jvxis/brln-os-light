package lndclient

import (
	"testing"

	"lightningos-light/lnrpc"
)

func TestSummarizePendingClosingBalances(t *testing.T) {
	resp := &lnrpc.PendingChannelsResponse{
		PendingClosingChannels: []*lnrpc.PendingChannelsResponse_ClosedChannel{
			{
				Channel: &lnrpc.PendingChannelsResponse_PendingChannel{
					LocalBalance: 11,
				},
			},
		},
		WaitingCloseChannels: []*lnrpc.PendingChannelsResponse_WaitingCloseChannel{
			{
				Channel: &lnrpc.PendingChannelsResponse_PendingChannel{
					LocalBalance: 22,
				},
				LimboBalance: 33,
			},
			{
				Channel: &lnrpc.PendingChannelsResponse_PendingChannel{
					LocalBalance: 44,
				},
			},
		},
		PendingForceClosingChannels: []*lnrpc.PendingChannelsResponse_ForceClosedChannel{
			{
				Channel: &lnrpc.PendingChannelsResponse_PendingChannel{
					LocalBalance: 100,
				},
				LimboBalance:      90,
				BlocksTilMaturity: 370,
			},
			{
				Channel: &lnrpc.PendingChannelsResponse_PendingChannel{
					LocalBalance: 75,
				},
				RecoveredBalance:  25,
				BlocksTilMaturity: 40,
			},
		},
	}

	got := summarizePendingClosingBalances(resp)

	if got.CoopClosingSat != 11 || got.CoopClosingCount != 1 {
		t.Fatalf("unexpected cooperative closing totals: sats=%d count=%d", got.CoopClosingSat, got.CoopClosingCount)
	}
	if got.WaitingCloseSat != 77 || got.WaitingCloseCount != 2 {
		t.Fatalf("unexpected waiting close totals: sats=%d count=%d", got.WaitingCloseSat, got.WaitingCloseCount)
	}
	if got.ForceClosingSat != 140 || got.ForceClosingCount != 2 {
		t.Fatalf("unexpected force close totals: sats=%d count=%d", got.ForceClosingSat, got.ForceClosingCount)
	}
	if got.ClosingPendingSat != 228 || got.ClosingPendingCount != 5 {
		t.Fatalf("unexpected aggregate closing totals: sats=%d count=%d", got.ClosingPendingSat, got.ClosingPendingCount)
	}
	if got.ForceClosingMinBlocksTilMaturity != 40 || got.ForceClosingMaxBlocksTilMaturity != 370 {
		t.Fatalf(
			"unexpected maturity range: min=%d max=%d",
			got.ForceClosingMinBlocksTilMaturity,
			got.ForceClosingMaxBlocksTilMaturity,
		)
	}
}

func TestSummarizePendingClosingBalancesEmpty(t *testing.T) {
	got := summarizePendingClosingBalances(nil)

	if got != (pendingClosingBalanceTotals{}) {
		t.Fatalf("expected empty totals, got %+v", got)
	}
}
