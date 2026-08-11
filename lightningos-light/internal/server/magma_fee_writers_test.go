package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The Magma fee ceiling was first enforced in applyChannelFeesWithRetry, on the
// belief that it was the only place a fee reached LND. It was not: Fee Center's
// Refresh writes to LND itself, so a refresh raised a sold channel above the fee
// its buyer had contracted, and Amboss bills that in fee_above_cap_seconds.
//
// There is no single choke point to defend, because the services hold a concrete
// *lndclient.Client rather than an interface. This test is the substitute: every
// call that can set a channel fee must be listed below with the reason it is
// safe. A new one fails the build until someone writes that reason down.
func TestEveryChannelFeeWriterHonoursTheMagmaCeiling(t *testing.T) {
	// file -> enclosing function -> why this call cannot breach a commitment.
	allowed := map[string]string{
		"autofee_service.go:applyChannelFeesWithRetry":     "clamped by magmaClampChannelFees before the call",
		"autofee_service.go:refreshReferenceFees":          "target and base fee both clamped by magmaClampChannelFees",
		"autofee_service.go:RestoreMarketRefillFeeSnapshot": "snapshot clamped by magmaClampChannelFees before replay",
		"handlers.go:handleLNUpdateFees":                   "single channel refused on breach; apply_all followed by ReapplyFeeCaps",
		"htlc_manager.go:tick":                             "carries the current policy fee through unchanged; only HTLC bounds move",
		"magma_fee_guard.go:enforceFeeCaps":                "this is the enforcement itself, and it only lowers",
	}

	writer := regexp.MustCompile(`\.(UpdateChannelFees|UpdateChannelPolicy)\(`)
	funcDecl := regexp.MustCompile(`^func (?:\([^)]*\) )?([A-Za-z0-9_]+)\(`)

	entries, err := os.ReadDir("./")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	found := make(map[string]bool)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("./", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		enclosing := ""
		for _, line := range strings.Split(string(raw), "\n") {
			if match := funcDecl.FindStringSubmatch(line); match != nil {
				enclosing = match[1]
			}
			if !writer.MatchString(line) {
				continue
			}
			key := name + ":" + enclosing
			found[key] = true
			if _, ok := allowed[key]; !ok {
				t.Errorf("%s writes a channel fee to LND but is not in the allowlist.\n"+
					"A channel sold on Magma is capped until its commitment ends. Either clamp the "+
					"fee with magmaClampChannelFees, or add %q with the reason it cannot breach.", key, key)
			}
		}
	}

	// A stale entry is its own problem: it suggests a protection that no longer
	// exists, and the next reader trusts it.
	for key := range allowed {
		if !found[key] {
			t.Errorf("%q is allowlisted but writes no channel fee any more; drop it", key)
		}
	}
}

// The clamp only ever lowers. If it could raise a fee, wiring it into the manual
// and refresh paths would let a commitment quietly push fees up.
func TestMagmaClampNeverRaisesAFee(t *testing.T) {
	const point = "aa11bb22cc33dd44ee55ff6677889900aabbccddeeff00112233445566778899:1"
	magmaCommitments.mu.Lock()
	magmaCommitments.byPoint = map[string]MagmaChannelCommitment{
		point: {OrderID: "o1", ChannelPoint: point, FeeRateCapPPM: 900, BaseFeeCapSat: 1},
	}
	magmaCommitments.byChannel = nil
	magmaCommitments.mu.Unlock()
	t.Cleanup(func() {
		magmaCommitments.mu.Lock()
		magmaCommitments.byPoint = nil
		magmaCommitments.mu.Unlock()
	})

	for _, in := range []struct{ base, rate int64 }{{0, 1}, {0, 100}, {500, 899}, {1000, 900}} {
		base, rate, _ := magmaClampChannelFees(0, point, in.base, in.rate)
		if base > in.base || rate > in.rate {
			t.Fatalf("clamp raised a fee: base %d->%d, rate %d->%d", in.base, base, in.rate, rate)
		}
	}
}
