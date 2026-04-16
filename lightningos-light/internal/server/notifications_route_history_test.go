package server

import (
	"testing"
	"time"

	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"
)

func TestBuildPaymentRouteHistoryCapturesAttemptsAndHopCosts(t *testing.T) {
	createdAt := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	attemptStartedAt := createdAt.Add(2 * time.Second)
	attemptResolvedAt := createdAt.Add(5 * time.Second)

	pay := &lnrpc.Payment{
		PaymentHash:    "ABC123",
		PaymentIndex:   42,
		Status:         lnrpc.Payment_SUCCEEDED,
		CreationTimeNs: createdAt.UnixNano(),
		Htlcs: []*lnrpc.HTLCAttempt{
			{
				AttemptId:     7,
				Status:        lnrpc.HTLCAttempt_SUCCEEDED,
				AttemptTimeNs: attemptStartedAt.UnixNano(),
				ResolveTimeNs: attemptResolvedAt.UnixNano(),
				Route: &lnrpc.Route{
					TotalAmtMsat:  101000,
					TotalFeesMsat: 1000,
					TotalTimeLock: 144,
					Hops: []*lnrpc.Hop{
						{ChanId: 11, PubKey: "hop-a", ChanCapacity: 1_000_000, AmtToForwardMsat: 100000, FeeMsat: 600, Expiry: 80},
						{ChanId: 12, PubKey: "hop-b", ChanCapacity: 2_000_000, AmtToForwardMsat: 99500, FeeMsat: 400, Expiry: 70},
						{ChanId: 13, PubKey: "hop-c", ChanCapacity: 3_000_000, AmtToForwardMsat: 99100, FeeMsat: 0, Expiry: 60},
					},
				},
			},
			{
				AttemptId: 8,
				Status:    lnrpc.HTLCAttempt_FAILED,
				Failure: &lnrpc.Failure{
					Code:               lnrpc.Failure_FEE_INSUFFICIENT,
					FailureSourceIndex: 2,
				},
			},
		},
	}

	attempts, hops := buildPaymentRouteHistory(pay, "lightning", createdAt)

	if got := len(attempts); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
	if got := len(hops); got != 3 {
		t.Fatalf("expected 3 hops, got %d", got)
	}

	first := attempts[0]
	if first.PaymentHash != "abc123" {
		t.Fatalf("expected normalized payment hash, got %q", first.PaymentHash)
	}
	if first.PaymentType != "lightning" {
		t.Fatalf("expected payment type lightning, got %q", first.PaymentType)
	}
	if first.PaymentStatus != "SUCCEEDED" {
		t.Fatalf("expected payment status SUCCEEDED, got %q", first.PaymentStatus)
	}
	if !first.PaymentCreatedAt.Equal(createdAt) {
		t.Fatalf("expected payment created at %v, got %v", createdAt, first.PaymentCreatedAt)
	}
	if !first.AttemptStartedAt.Equal(attemptStartedAt) {
		t.Fatalf("expected attempt start %v, got %v", attemptStartedAt, first.AttemptStartedAt)
	}
	if !first.AttemptResolvedAt.Equal(attemptResolvedAt) {
		t.Fatalf("expected attempt resolve %v, got %v", attemptResolvedAt, first.AttemptResolvedAt)
	}
	if first.TotalAmtMsat != 101000 {
		t.Fatalf("expected total amount 101000 msat, got %d", first.TotalAmtMsat)
	}
	if first.TotalFeeMsat != 1000 {
		t.Fatalf("expected total fee 1000 msat, got %d", first.TotalFeeMsat)
	}
	if first.TotalTimeLock != 144 {
		t.Fatalf("expected total timelock 144, got %d", first.TotalTimeLock)
	}
	if first.HopCount != 3 {
		t.Fatalf("expected hop count 3, got %d", first.HopCount)
	}

	failed := attempts[1]
	if failed.AttemptStatus != "FAILED" {
		t.Fatalf("expected failed attempt status, got %q", failed.AttemptStatus)
	}
	if failed.FailureCode != "FEE_INSUFFICIENT" {
		t.Fatalf("expected failure code FEE_INSUFFICIENT, got %q", failed.FailureCode)
	}
	if failed.FailureSourceIndex != 2 {
		t.Fatalf("expected failure source index 2, got %d", failed.FailureSourceIndex)
	}
	if failed.HopCount != 0 {
		t.Fatalf("expected failed attempt without route to have zero hops, got %d", failed.HopCount)
	}

	if hops[0].CostToMsat != 0 || !hops[0].IsFirstHop || hops[0].IsFinalHop {
		t.Fatalf("unexpected first hop metadata: %+v", hops[0])
	}
	if hops[1].CostToMsat != 600 || hops[1].IsFirstHop || hops[1].IsFinalHop {
		t.Fatalf("unexpected second hop metadata: %+v", hops[1])
	}
	if hops[2].CostToMsat != 1000 || hops[2].IsFirstHop || !hops[2].IsFinalHop {
		t.Fatalf("unexpected final hop metadata: %+v", hops[2])
	}
}

func TestPaymentRouteTypePriority(t *testing.T) {
	keysendPay := &lnrpc.Payment{
		Htlcs: []*lnrpc.HTLCAttempt{
			{
				Route: &lnrpc.Route{
					Hops: []*lnrpc.Hop{
						{CustomRecords: map[uint64][]byte{lndclient.KeysendPreimageRecord: []byte{1, 2, 3}}},
					},
				},
			},
		},
	}

	if got := paymentRouteType(keysendPay, true, false); got != "keysend" {
		t.Fatalf("expected keysend priority, got %q", got)
	}
	if got := paymentRouteType(keysendPay, true, true); got != "rebalance" {
		t.Fatalf("expected rebalance to override keysend, got %q", got)
	}
	if got := paymentRouteType(&lnrpc.Payment{}, false, false); got != "probe" {
		t.Fatalf("expected probe classification for empty payment request/keysend-free payment, got %q", got)
	}
}
