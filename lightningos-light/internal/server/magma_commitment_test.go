package server

import "testing"

// The clamp is the last gate before a fee reaches LND, and Amboss bills time
// spent above the ceiling in fee_above_cap_seconds. A miss here is measured
// against the account.
func TestMagmaClampChannelFees(t *testing.T) {
	const soldPoint = "aa11bb22cc33dd44ee55ff6677889900aabbccddeeff00112233445566778899:1"
	const soldChannel = uint64(1055659805681319937)

	seed := func(commitment MagmaChannelCommitment) {
		magmaCommitments.mu.Lock()
		magmaCommitments.byPoint = map[string]MagmaChannelCommitment{soldPoint: commitment}
		magmaCommitments.byChannel = map[uint64]MagmaChannelCommitment{soldChannel: commitment}
		magmaCommitments.mu.Unlock()
	}
	t.Cleanup(func() {
		magmaCommitments.mu.Lock()
		magmaCommitments.byPoint = nil
		magmaCommitments.byChannel = nil
		magmaCommitments.mu.Unlock()
	})

	seed(MagmaChannelCommitment{
		OrderID: "order-1", ChannelPoint: soldPoint,
		FeeRateCapPPM: 900, BaseFeeCapSat: 1,
	})

	t.Run("fee above the ceiling is pulled under it", func(t *testing.T) {
		base, rate, clamped := magmaClampChannelFees(soldChannel, soldPoint, 1000, 2500)
		if !clamped {
			t.Fatal("expected the fee to be capped")
		}
		if rate >= 900 {
			t.Fatalf("rate %d must land below the 900 ppm ceiling", rate)
		}
		if base != 1000 {
			t.Fatalf("base fee of 1000 msat is exactly the 1 sat ceiling: got %d", base)
		}
	})

	t.Run("fee under the ceiling is left alone", func(t *testing.T) {
		base, rate, clamped := magmaClampChannelFees(soldChannel, soldPoint, 0, 450)
		if clamped {
			t.Fatal("a compliant fee must not be touched")
		}
		if rate != 450 || base != 0 {
			t.Fatalf("values changed: base %d rate %d", base, rate)
		}
	})

	t.Run("base fee above the ceiling is pulled down", func(t *testing.T) {
		base, _, clamped := magmaClampChannelFees(soldChannel, soldPoint, 5000, 100)
		if !clamped || base != 1000 {
			t.Fatalf("base fee should be capped at 1 sat: got %d (clamped=%t)", base, clamped)
		}
	})

	// The commonest real case: more than half of the orders on this account cap
	// the base fee at zero, so any base fee at all is a breach.
	t.Run("a zero base fee ceiling forbids any base fee", func(t *testing.T) {
		seed(MagmaChannelCommitment{OrderID: "order-2", ChannelPoint: soldPoint,
			FeeRateCapPPM: 500, BaseFeeCapSat: 0})
		base, _, clamped := magmaClampChannelFees(soldChannel, soldPoint, 1000, 100)
		if !clamped || base != 0 {
			t.Fatalf("expected the base fee to be zeroed, got %d", base)
		}
	})

	// Matching by channel point matters because the numeric id is only known
	// after the channel confirms.
	t.Run("matches by channel point when the id is unknown", func(t *testing.T) {
		seed(MagmaChannelCommitment{OrderID: "order-3", ChannelPoint: soldPoint,
			FeeRateCapPPM: 900, BaseFeeCapSat: 1})
		_, rate, clamped := magmaClampChannelFees(0, soldPoint, 0, 2500)
		if !clamped || rate >= 900 {
			t.Fatalf("point lookup failed: rate %d clamped %t", rate, clamped)
		}
	})

	// Every other channel on the node has to pass through untouched.
	t.Run("channels with no commitment are untouched", func(t *testing.T) {
		base, rate, clamped := magmaClampChannelFees(999, "deadbeef:0", 1000, 5000)
		if clamped || base != 1000 || rate != 5000 {
			t.Fatalf("unrelated channel was modified: base %d rate %d clamped %t", base, rate, clamped)
		}
	})

	// An empty cache means "nothing known", never "cap everything to zero".
	t.Run("an empty cache changes nothing", func(t *testing.T) {
		magmaCommitments.mu.Lock()
		magmaCommitments.byPoint = nil
		magmaCommitments.byChannel = nil
		magmaCommitments.mu.Unlock()
		base, rate, clamped := magmaClampChannelFees(soldChannel, soldPoint, 1000, 5000)
		if clamped || base != 1000 || rate != 5000 {
			t.Fatalf("empty cache must be a no-op: base %d rate %d clamped %t", base, rate, clamped)
		}
	})
}
