package lndclient

import (
	"testing"
	"time"

	"lightningos-light/lnrpc"
)

func TestSnapshotPendingOpenSinceKeepsFirstSeenUntilRemoved(t *testing.T) {
	client := &Client{}
	first := time.Unix(1_700_000_000, 0).UTC()
	second := first.Add(2 * time.Hour)

	firstSeen := client.snapshotPendingOpenSince([]string{"abc:0", "def:1"}, first)
	if got := firstSeen[normalizeChannelPointKey("abc:0")]; !got.Equal(first) {
		t.Fatalf("expected first seen time %v, got %v", first, got)
	}

	secondSeen := client.snapshotPendingOpenSince([]string{"abc:0"}, second)
	if got := secondSeen[normalizeChannelPointKey("abc:0")]; !got.Equal(first) {
		t.Fatalf("expected existing point to keep first seen time %v, got %v", first, got)
	}
	if _, ok := secondSeen[normalizeChannelPointKey("def:1")]; ok {
		t.Fatalf("expected removed point to be dropped from snapshot")
	}
}

func TestDetectPendingOpenBumpCandidatesEligibleWithWalletChange(t *testing.T) {
	results := detectPendingOpenBumpCandidates(
		true,
		[]*lnrpc.Transaction{
			{
				TxHash: "1111111111111111111111111111111111111111111111111111111111111111",
				OutputDetails: []*lnrpc.OutputDetail{
					{OutputIndex: 0, Amount: 500_000, IsOurAddress: false},
					{OutputIndex: 1, Amount: 120_000, IsOurAddress: true},
				},
			},
		},
		true,
		[]*lnrpc.Utxo{
			{
				AmountSat: 120_000,
				Outpoint: &lnrpc.OutPoint{
					TxidStr:     "1111111111111111111111111111111111111111111111111111111111111111",
					OutputIndex: 1,
				},
			},
		},
		[]string{"1111111111111111111111111111111111111111111111111111111111111111:0"},
	)

	candidate := results[normalizeChannelPointKey("1111111111111111111111111111111111111111111111111111111111111111:0")]
	if !candidate.Checked {
		t.Fatalf("expected bump candidate to be checked")
	}
	if !candidate.Eligible {
		t.Fatalf("expected bump candidate to be eligible")
	}
	if candidate.Outpoint != "1111111111111111111111111111111111111111111111111111111111111111:1" {
		t.Fatalf("unexpected outpoint: %q", candidate.Outpoint)
	}
	if candidate.AmountSat != 120_000 {
		t.Fatalf("unexpected candidate amount: %d", candidate.AmountSat)
	}
}

func TestDetectPendingOpenBumpCandidatesNoWalletOutput(t *testing.T) {
	results := detectPendingOpenBumpCandidates(
		true,
		[]*lnrpc.Transaction{
			{
				TxHash: "2222222222222222222222222222222222222222222222222222222222222222",
				OutputDetails: []*lnrpc.OutputDetail{
					{OutputIndex: 0, Amount: 400_000, IsOurAddress: false},
				},
			},
		},
		true,
		nil,
		[]string{"2222222222222222222222222222222222222222222222222222222222222222:0"},
	)

	candidate := results[normalizeChannelPointKey("2222222222222222222222222222222222222222222222222222222222222222:0")]
	if !candidate.Checked {
		t.Fatalf("expected bump candidate to be checked")
	}
	if candidate.Eligible {
		t.Fatalf("expected bump candidate to be ineligible")
	}
	if candidate.Reason != pendingOpenBumpReasonNoWalletOutput {
		t.Fatalf("unexpected reason: %q", candidate.Reason)
	}
}

func TestDetectPendingOpenBumpCandidatesWalletOutputUnavailable(t *testing.T) {
	results := detectPendingOpenBumpCandidates(
		true,
		[]*lnrpc.Transaction{
			{
				TxHash: "3333333333333333333333333333333333333333333333333333333333333333",
				OutputDetails: []*lnrpc.OutputDetail{
					{OutputIndex: 0, Amount: 600_000, IsOurAddress: false},
					{OutputIndex: 1, Amount: 75_000, IsOurAddress: true},
				},
			},
		},
		true,
		nil,
		[]string{"3333333333333333333333333333333333333333333333333333333333333333:0"},
	)

	candidate := results[normalizeChannelPointKey("3333333333333333333333333333333333333333333333333333333333333333:0")]
	if !candidate.Checked {
		t.Fatalf("expected bump candidate to be checked")
	}
	if candidate.Eligible {
		t.Fatalf("expected bump candidate to be ineligible")
	}
	if candidate.Reason != pendingOpenBumpReasonWalletOutputUnavailable {
		t.Fatalf("unexpected reason: %q", candidate.Reason)
	}
}
