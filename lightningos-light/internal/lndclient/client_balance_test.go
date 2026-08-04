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

func TestApplyChannelBalanceMapsModernFields(t *testing.T) {
	summary := BalanceSummary{}
	applyChannelBalance(&summary, &lnrpc.ChannelBalanceResponse{
		Balance:                  100,
		LocalBalance:             &lnrpc.Amount{Sat: 101},
		RemoteBalance:            &lnrpc.Amount{Sat: 202},
		UnsettledLocalBalance:    &lnrpc.Amount{Sat: 3},
		UnsettledRemoteBalance:   &lnrpc.Amount{Sat: 4},
		PendingOpenLocalBalance:  &lnrpc.Amount{Sat: 5},
		PendingOpenRemoteBalance: &lnrpc.Amount{Sat: 6},
	})

	if summary.LightningSat != 100 || summary.LightningLocalSat != 101 {
		t.Fatalf("unexpected local balances: legacy=%d local=%d", summary.LightningSat, summary.LightningLocalSat)
	}
	if summary.LightningRemoteSat != 202 || summary.LightningUnsettledLocalSat != 3 || summary.LightningUnsettledRemoteSat != 4 {
		t.Fatalf("unexpected channel balances: %#v", summary)
	}
	if summary.LightningPendingOpenLocalSat != 5 || summary.LightningPendingOpenRemoteSat != 6 {
		t.Fatalf("unexpected pending-open balances: %#v", summary)
	}
}

func TestApplyChannelBalanceFallsBackToLegacyLocalBalance(t *testing.T) {
	summary := BalanceSummary{}
	applyChannelBalance(&summary, &lnrpc.ChannelBalanceResponse{Balance: 123})
	if summary.LightningLocalSat != 123 {
		t.Fatalf("local balance = %d, want legacy fallback 123", summary.LightningLocalSat)
	}
}
