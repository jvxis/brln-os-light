package reports

import (
	"testing"

	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"
)

// A keysend carries no invoice, so the only marker is the preimage LND puts in
// the custom records. It can arrive in two shapes, and a multi-part payment
// carries it on every shard.
func TestPaymentIsKeysend(t *testing.T) {
	keysendRecord := map[uint64][]byte{lndclient.KeysendPreimageRecord: {0x01}}

	t.Run("marker on the first hop records", func(t *testing.T) {
		pay := &lnrpc.Payment{FirstHopCustomRecords: keysendRecord}
		if !paymentIsKeysend(pay) {
			t.Fatal("a first-hop preimage record marks a keysend")
		}
	})

	t.Run("marker on the last hop of an attempt", func(t *testing.T) {
		pay := &lnrpc.Payment{Htlcs: []*lnrpc.HTLCAttempt{{
			Route: &lnrpc.Route{Hops: []*lnrpc.Hop{
				{},
				{CustomRecords: keysendRecord},
			}},
		}}}
		if !paymentIsKeysend(pay) {
			t.Fatal("the record sits on the final hop, where the recipient reads it")
		}
	})

	t.Run("only the final hop counts", func(t *testing.T) {
		// A record on an intermediate hop is not a keysend to the destination.
		pay := &lnrpc.Payment{Htlcs: []*lnrpc.HTLCAttempt{{
			Route: &lnrpc.Route{Hops: []*lnrpc.Hop{
				{CustomRecords: keysendRecord},
				{},
			}},
		}}}
		if paymentIsKeysend(pay) {
			t.Fatal("an intermediate hop record must not be read as a keysend")
		}
	})

	t.Run("an ordinary invoice payment is not one", func(t *testing.T) {
		pay := &lnrpc.Payment{Htlcs: []*lnrpc.HTLCAttempt{{
			Route: &lnrpc.Route{Hops: []*lnrpc.Hop{{}, {}}},
		}}}
		if paymentIsKeysend(pay) {
			t.Fatal("no preimage record means no keysend")
		}
	})

	t.Run("nil and empty are safe", func(t *testing.T) {
		if paymentIsKeysend(nil) {
			t.Fatal("nil payment")
		}
		if paymentIsKeysend(&lnrpc.Payment{}) {
			t.Fatal("empty payment")
		}
		if paymentIsKeysend(&lnrpc.Payment{Htlcs: []*lnrpc.HTLCAttempt{nil}}) {
			t.Fatal("nil attempt")
		}
		if paymentIsKeysend(&lnrpc.Payment{Htlcs: []*lnrpc.HTLCAttempt{{Route: &lnrpc.Route{}}}}) {
			t.Fatal("route with no hops")
		}
	})
}

// Keysend sent is a cost, and it is the amount that costs - the fee it paid is
// already counted with every other payment fee, so adding it here would charge
// the same sats twice.
func TestKeysendSentEntersTheTotalCostOnce(t *testing.T) {
	m := Metrics{
		ForwardFeeRevenueMsat: 1_000_000,
		PaymentFeeCostMsat:    5_000, // includes the fee the keysend itself paid
		KeysendSentMsat:       200_000,
	}.withNetTotal()

	if want := int64(5_000 + 200_000); m.TotalCostMsat() != want {
		t.Fatalf("total cost: got %d, want %d", m.TotalCostMsat(), want)
	}
	if want := int64(1_000_000 - 205_000); m.NetTotalMsat != want {
		t.Fatalf("net total: got %d, want %d", m.NetTotalMsat, want)
	}
	// Routing is untouched: giving sats away says nothing about forwarding.
	if m.ForwardFeeRevenueMsat-m.RebalanceFeeCostMsat != 1_000_000 {
		t.Fatal("keysend sent must not reach the routing figure")
	}
}
