package server

import (
	"strings"
	"testing"
	"time"
)

func magmaSaneTestPolicy() MagmaPolicy {
	policy := defaultMagmaPolicy()
	policy.MinRevenueSat, policy.MinPricePPM, policy.MinPricePPMPerDay = 0, 0, 0
	policy.MinFeeRateCapPPM = 0
	return policy
}

func magmaSaneTestOrder() magmaPolicyInputs {
	return magmaPolicyInputs{
		Order:            MagmaOrder{SizeSat: 1_500_000, RevenueSat: 6000, CommitmentBlocks: 4320},
		AvailableSat:     9_000_000,
		OnchainReachable: true,
		SatPerVbyte:      2,
		EstimatedFeeSat:  300,
	}
}

// A buyer whose node is down is waited for, not refused. LND will not open a
// channel to a peer that is not connected, so accepting while it is down
// promises a delivery on a connection we do not have - but an unreachable buyer
// has been observed paying in full, so it predicts nothing either. Waiting costs
// nothing while there is window left.
func TestMagmaUnreachableBuyerDefersRatherThanRefuses(t *testing.T) {
	in := magmaSaneTestOrder()
	in.BuyerUnreachable = true
	in.PendingFor = time.Minute

	d := evaluateMagmaOrder(magmaSaneTestPolicy(), in)
	if d.Accept {
		t.Fatal("must not accept while the peer needed to deliver is down")
	}
	if d.Reject {
		t.Fatalf("must not refuse either, there is still time: %s", d.Reason)
	}
}

// The zero value has to be the harmless one. A caller that never learned whether
// the buyer answered must not silently stop every sale.
func TestMagmaUnknownReachabilityDoesNotBlock(t *testing.T) {
	if d := evaluateMagmaOrder(magmaSaneTestPolicy(), magmaSaneTestOrder()); !d.Accept {
		t.Fatalf("an order nobody flagged must still be accepted: %s", d.Reason)
	}
}

// Terms that can never be met are refused on their own account. Deferring them
// for a peer would spend the window waiting on a node whose order we were never
// going to take.
func TestMagmaBadTermsAreRefusedEvenWithTheBuyerDown(t *testing.T) {
	policy := magmaSaneTestPolicy()
	policy.MaxChannelSizeSat = 1_000_000

	in := magmaSaneTestOrder()
	in.BuyerUnreachable = true

	d := evaluateMagmaOrder(policy, in)
	if !d.Reject {
		t.Fatalf("an oversized channel is refused whatever the peer is doing: %s", d.Reason)
	}
}

// Amboss's own deadline replaces the estimate when we have it. The grace period
// was derived from a single order that lapsed 2h04m after creation; a published
// instant is not a guess.
func TestMagmaRealDeadlineDrivesTheRefusal(t *testing.T) {
	policy := magmaSaneTestPolicy()
	now := time.Now()

	plenty := magmaSaneTestOrder()
	plenty.BuyerUnreachable = true
	plenty.PendingFor = 5 * time.Minute
	far := now.Add(90 * time.Minute)
	plenty.Deadline = &far
	if d := evaluateMagmaOrder(policy, plenty); d.Reject {
		t.Fatalf("with 90 minutes left the order is still worth waiting for: %s", d.Reason)
	}

	// Refusing happens with time to spare, deliberately before the deadline:
	// refusing at it is not refusing at all, it is the lapse Amboss records.
	closing := magmaSaneTestOrder()
	closing.BuyerUnreachable = true
	closing.PendingFor = 5 * time.Minute
	near := now.Add(20 * time.Minute)
	closing.Deadline = &near
	d := evaluateMagmaOrder(policy, closing)
	if !d.Reject {
		t.Fatalf("close to the deadline the refusal must be explicit: %s", d.Reason)
	}
	if !strings.Contains(d.Reason, "refusing explicitly") {
		t.Fatalf("the reason should say what it did, got %q", d.Reason)
	}
}

// A short PendingFor with no published deadline still falls back to the observed
// window, so nothing regresses on orders Amboss gives no instant for.
func TestMagmaFallsBackToTheObservedWindow(t *testing.T) {
	policy := magmaSaneTestPolicy()

	fresh := magmaSaneTestOrder()
	fresh.BuyerUnreachable = true
	fresh.PendingFor = time.Minute
	if d := evaluateMagmaOrder(policy, fresh); d.Reject {
		t.Fatalf("a minute in, the order has almost all its window: %s", d.Reason)
	}

	late := magmaSaneTestOrder()
	late.BuyerUnreachable = true
	late.PendingFor = magmaApprovalGrace + time.Minute
	if d := evaluateMagmaOrder(policy, late); !d.Reject {
		t.Fatalf("past the grace period it must be refused explicitly: %s", d.Reason)
	}
}
