package main

import (
	"testing"

	"lightningos-light/internal/reports"
)

func TestReportsBackfillDryRunComponentsIncludesKeysendSent(t *testing.T) {
	stored := reports.Metrics{
		ForwardFeeRevenueSat: 1,
		RebalanceFeeCostSat:  2,
		PaymentFeeCostSat:    3,
		OnchainFeeCostSat:    4,
		KeysendReceivedSat:   5,
		KeysendSentSat:       6,
	}
	computed := reports.Metrics{
		ForwardFeeRevenueSat: 11,
		RebalanceFeeCostSat:  12,
		PaymentFeeCostSat:    13,
		OnchainFeeCostSat:    14,
		KeysendReceivedSat:   15,
		KeysendSentSat:       16,
	}

	components := reportsBackfillDryRunComponents(stored, computed)
	wantNames := []string{"forwards", "rebalances", "payments", "onchain", "keysend", "keysend-sent"}
	if len(components) != len(wantNames) {
		t.Fatalf("got %d dry-run components, want %d", len(components), len(wantNames))
	}
	for index, wantName := range wantNames {
		if components[index].name != wantName {
			t.Fatalf("component %d = %q, want %q", index, components[index].name, wantName)
		}
	}
	keysendSent := components[len(components)-1]
	if keysendSent.stored != 6 || keysendSent.computed != 16 {
		t.Fatalf("unexpected keysend sent comparison: %+v", keysendSent)
	}
}
