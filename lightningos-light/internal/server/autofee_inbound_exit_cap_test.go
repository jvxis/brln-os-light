package server

import (
	"reflect"
	"testing"
)

// The 14/11 shape: a 10M sink priced at 3.500 ppm with a 1.869 ppm discount,
// whose inbound flow left through three paid-refill sellers priced at
// 1.327 / 1.600 / 1.699 ppm (44% / 25% / 24% of the volume) and a few tiny
// exits. Numbers are shapes, not channel identities.
func exitCapFixture() (map[uint64]int64, func(uint64) bool, func(uint64) int) {
	exits := map[uint64]int64{
		101: 3_780_000_000, // paid seller, 1327 ppm
		102: 2_170_000_000, // paid seller, 1600 ppm
		103: 2_070_000_000, // paid seller, 1699 ppm
		104: 480_000_000,   // paid seller, 3206 ppm (above the discount anyway)
		105: 10_000_000,    // paid seller, 106 ppm, 0.1% share: ignored
		106: 50_000_000,    // source, 90 ppm: never bounds
	}
	paid := func(id uint64) bool { return id != 106 }
	fee := func(id uint64) int {
		return map[uint64]int{101: 1327, 102: 1600, 103: 1699, 104: 3206, 105: 106, 106: 90}[id]
	}
	return exits, paid, fee
}

func TestInboundDiscountExitCapBoundsByCheapestRelevantPaidExit(t *testing.T) {
	exits, paid, fee := exitCapFixture()
	capPpm, exit, exitFee, share := inboundDiscountExitCap(exits, paid, fee, 0.10, 0.12)
	if exit != 101 || exitFee != 1327 {
		t.Fatalf("expected the 1327 ppm seller to bound, got exit=%d fee=%d", exit, exitFee)
	}
	if capPpm != 1167 {
		t.Fatalf("expected cap 1327*(1-0.12)=1167, got %d", capPpm)
	}
	if share < 0.44 || share > 0.45 {
		t.Fatalf("unexpected share %.3f", share)
	}
}

func TestInboundDiscountExitCapIgnoresSourcesAndTinyExits(t *testing.T) {
	exits, paid, fee := exitCapFixture()
	// Only the two cheap exits are sources now: the cap moves to the next paid one.
	paidNoCheap := func(id uint64) bool { return paid(id) && id != 101 && id != 102 }
	capPpm, exit, _, _ := inboundDiscountExitCap(exits, paidNoCheap, fee, 0.10, 0.12)
	if exit != 103 || capPpm != 1495 {
		t.Fatalf("expected the 1699 ppm seller (cap 1495), got exit=%d cap=%d", exit, capPpm)
	}
	// The 106 ppm exit is paid but carries 0.1% of the flow: ignored at any
	// profile share, counted once the share floor drops below it.
	capPpm, exit, _, _ = inboundDiscountExitCap(exits, paid, fee, 0.0005, 0.12)
	if exit != 105 || capPpm != 93 {
		t.Fatalf("expected the tiny exit to count below its share, got exit=%d cap=%d", exit, capPpm)
	}
}

func TestInboundDiscountExitCapProfilesAndOff(t *testing.T) {
	exits, paid, fee := exitCapFixture()
	// 104 carries 5.6%: conservative (5%) counts it, moderate (10%) does not;
	// it is the dearest exit so the cap itself stays on the 1327 seller.
	for _, tc := range []struct {
		name  string
		share float64
		want  int
	}{
		{"conservative", autofeeProfiles["conservative"].InboundDiscountExitShareMin, 1327 * 80 / 100},
		{"moderate", autofeeProfiles["moderate"].InboundDiscountExitShareMin, 1167},
		{"aggressive", autofeeProfiles["aggressive"].InboundDiscountExitShareMin, 1220},
	} {
		spread := autofeeProfiles[tc.name].InboundDiscountMinRetainedSpreadFrac
		capPpm, exit, _, _ := inboundDiscountExitCap(exits, paid, fee, tc.share, spread)
		if exit != 101 || capPpm != tc.want {
			t.Fatalf("%s: expected cap %d on exit 101, got cap=%d exit=%d", tc.name, tc.want, capPpm, exit)
		}
	}
	if capPpm, exit, _, _ := inboundDiscountExitCap(exits, paid, fee, 1.0, 0.12); capPpm != 0 || exit != 0 {
		t.Fatalf("share 100%% must switch the cap off, got cap=%d exit=%d", capPpm, exit)
	}
	if capPpm, exit, _, _ := inboundDiscountExitCap(nil, paid, fee, 0.10, 0.12); capPpm != 0 || exit != 0 {
		t.Fatalf("no inbound history must mean no cap, got cap=%d exit=%d", capPpm, exit)
	}
}

func TestInboundDiscountExitCapForPicksLowerWindow(t *testing.T) {
	engine := &autofeeEngine{
		cfg:     AutofeeConfig{InboundPassiveEnabled: true},
		profile: autofeeProfiles["moderate"],
		channelFeePpm: map[uint64]int{
			101: 1327, 102: 1600,
		},
		inboundExits7d:  map[uint64]map[uint64]int64{7: {102: 2_000_000_000}},
		inboundExits21d: map[uint64]map[uint64]int64{7: {101: 3_000_000_000, 102: 2_000_000_000}},
	}
	paid := rebalStats{ByChannel: map[uint64]rebalStat{101: {FeeMsat: 1, Count: 1}, 102: {FeeMsat: 1, Count: 1}}}
	got := engine.inboundDiscountExitCapFor(7, paid, paid, 0.12)
	if got.Window != "21d" || got.ExitChannelID != 101 || got.Ppm != 1167 {
		t.Fatalf("expected the 21d window with the 1327 exit to win, got %+v", got)
	}
	// Override at 100% switches it off through the config path.
	engine.cfg.InboundDiscountExitShareMinOverride = 1.0
	if got := engine.inboundDiscountExitCapFor(7, paid, paid, 0.12); got.Ppm != 0 || got.ExitChannelID != 0 {
		t.Fatalf("override 100%% should disable the cap, got %+v", got)
	}
}

func TestAdvanceBalancedInboundDiscountLifecycleCapAndFullCut(t *testing.T) {
	// Grace keeps the discount but the exit cap still bounds it.
	discount, rounds, tags := advanceBalancedInboundDiscountLifecycle(0, 1869, 3500, 0.90, 0, inboundExitCapResult{Ppm: 1167, ExitChannelID: 101}, false)
	if discount != 1167 || rounds != 1 || !reflect.DeepEqual(tags, []string{"inbound-exit-cap", "inbound-grace"}) {
		t.Fatalf("expected capped grace, got discount=%d rounds=%d tags=%v", discount, rounds, tags)
	}
	// Channel at or above its own rebalance target: dropped at once.
	discount, rounds, tags = advanceBalancedInboundDiscountLifecycle(0, 1869, 3500, 0.90, 0, inboundExitCapResult{Ppm: 1167, ExitChannelID: 101}, true)
	if discount != 0 || rounds != 1 || !reflect.DeepEqual(tags, []string{"inbound-cut-full"}) {
		t.Fatalf("expected full cut, got discount=%d rounds=%d tags=%v", discount, rounds, tags)
	}
	// A fresh eligible discount is untouched here (the cap is applied before).
	if discount, _, _ := advanceBalancedInboundDiscountLifecycle(400, 600, 1000, 0.90, 3, inboundExitCapResult{Ppm: 100, ExitChannelID: 101}, true); discount != 400 {
		t.Fatalf("eligible candidate must pass through, got %d", discount)
	}
}

func TestInboundDiscountFullCutUsesTheChannelTarget(t *testing.T) {
	if inboundDiscountFullCut(0.99, autofeeRebalanceRuntimeSnapshot{}) {
		t.Fatalf("no target known: never cut")
	}
	if !inboundDiscountFullCut(0.99, autofeeRebalanceRuntimeSnapshot{TargetOutboundPct: 70}) {
		t.Fatalf("99%% local against a 70%% target must cut")
	}
	if inboundDiscountFullCut(0.50, autofeeRebalanceRuntimeSnapshot{TargetOutboundPct: 70}) {
		t.Fatalf("50%% local against a 70%% target must not cut")
	}
}

func TestSelectAutofeeRefreshInboundDiscountHonoursExitCap(t *testing.T) {
	cfg := AutofeeConfig{InboundPassiveEnabled: true}
	profile := autofeeProfiles["moderate"]
	forward7d := forwardStat{FeeMsat: 3_000_000 * 3000, AmtMsat: 3_000_000 * 1000, Count: 12}
	uncapped, source, apply := selectAutofeeRefreshInboundDiscount(cfg, profile, "sink", 0.05, 0.05, forwardStat{}, forward7d, 10_000_000, 1100, 3500, inboundExitCapResult{})
	if !apply || uncapped <= 1167 {
		t.Fatalf("fixture must produce a discount above the cap, got %d (%s, apply=%v)", uncapped, source, apply)
	}
	capped, source, apply := selectAutofeeRefreshInboundDiscount(cfg, profile, "sink", 0.05, 0.05, forwardStat{}, forward7d, 10_000_000, 1100, 3500, inboundExitCapResult{Ppm: 1167, ExitChannelID: 101})
	if !apply || capped != 1167 || source != "balanced+exit-cap" {
		t.Fatalf("expected capped preview 1167 (balanced+exit-cap), got %d (%s, apply=%v)", capped, source, apply)
	}
}

func TestInboundDiscountZeroRoundedPaidExitCapIsNotUnlimited(t *testing.T) {
	capPpm, exit, _, _ := inboundDiscountExitCap(map[uint64]int64{101: 1000}, func(uint64) bool { return true }, func(uint64) int { return 1 }, 0.10, 0.12)
	if capPpm != 0 || exit != 101 {
		t.Fatalf("fixture must yield an active zero cap, got cap=%d exit=%d", capPpm, exit)
	}
	forward7d := forwardStat{FeeMsat: 3_000_000 * 3000, AmtMsat: 3_000_000 * 1000, Count: 12}
	cap := inboundExitCapResult{Ppm: capPpm, ExitChannelID: exit}
	discount, _, apply := selectAutofeeRefreshInboundDiscount(AutofeeConfig{InboundPassiveEnabled: true}, autofeeProfiles["moderate"], "sink", 0.05, 0.05, forwardStat{}, forward7d, 10_000_000, 1100, 3500, cap)
	if !apply || discount != 0 {
		t.Fatalf("a zero cap on a paid exit must clear the discount, not mean unlimited: discount=%d apply=%v", discount, apply)
	}
	discount, _, tags := advanceBalancedInboundDiscountLifecycle(0, 1977, 3500, 0.90, 0, cap, false)
	if discount != 0 || !reflect.DeepEqual(tags, []string{"inbound-exit-cap"}) {
		t.Fatalf("zero cap must also prevent grace from retaining the discount: discount=%d tags=%v", discount, tags)
	}
}
