package server

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"lightningos-light/internal/lndclient"
)

// A node lost six hours of forwards on 2026-08-15. Ingestion order proved it:
// the forward that occurred at 01:02 was written to the database after the
// rebalance that occurred at 07:22, and sixteen forwards spanning 01:02 to
// 04:52 landed in a single batch.
//
// The rebalances survived the same window because runPayments only considers
// idling when no payment is in flight, and a busy node always has one. That was
// luck, not design: runForwards had no such guard, slept five minutes and then
// skipped the cycle outright.
func TestDegradedRuntimeSlowsIngestionButNeverSkipsIt(t *testing.T) {
	raw, err := os.ReadFile("notifications.go")
	if err != nil {
		t.Fatalf("read notifications.go: %v", err)
	}
	source := string(raw)

	for _, loop := range []string{"runForwards", "runPayments", "runPendingChannels"} {
		start := strings.Index(source, "func (n *Notifier) "+loop+"(")
		if start < 0 {
			t.Fatalf("%s not found", loop)
		}
		end := strings.Index(source[start+1:], "\nfunc ")
		if end < 0 {
			t.Fatalf("could not bound %s", loop)
		}
		body := source[start : start+1+end]

		// The skip must never be reachable from the degraded signal. Slowing the
		// interval is fine; standing still is what loses events.
		skip := regexp.MustCompile(`(?s)if\s+[^{]*degraded[^{]*\{[^}]*continue`)
		if skip.MatchString(body) {
			t.Errorf("%s can skip a cycle because the runtime looks degraded; "+
				"an unread forward is an unrecorded forward", loop)
		}
		if !strings.Contains(body, "degraded") {
			t.Errorf("%s no longer distinguishes a degraded runtime from an idle "+
				"node; the two must not share one flag", loop)
		}
	}
}

// A zero-value RuntimeInfo reports no channels. Reading that as "nothing to do"
// is what let an unknown runtime pass for an idle node.
func TestUnknownRuntimeIsNotAnIdleNode(t *testing.T) {
	var unknown lndclient.RuntimeInfo
	if unknown.Known {
		t.Fatal("the zero value must not claim to be known")
	}
	if unknown.NumActiveChannels != 0 || unknown.NumPendingChannels != 0 {
		t.Fatal("the zero value has no channels; that is the trap")
	}

	// The guarded form: unknown never counts as quiet.
	quiet := unknown.Known && unknown.NumActiveChannels < 2 && unknown.NumPendingChannels == 0
	if quiet {
		t.Fatal("an unknown runtime must not be treated as a node with no channels")
	}

	// A genuinely idle node still is.
	idleNode := lndclient.RuntimeInfo{Known: true}
	if !(idleNode.Known && idleNode.NumActiveChannels < 2 && idleNode.NumPendingChannels == 0) {
		t.Fatal("a known node with no channels has no forwards and may be skipped")
	}
}

// The condition that triggered the outage, kept explicit so its meaning is not
// quietly widened later.
func TestDeferNonessentialCovers(t *testing.T) {
	cases := []struct {
		name string
		info lndclient.RuntimeInfo
		want bool
	}{
		{"unknown", lndclient.RuntimeInfo{}, true},
		{"stale reading", lndclient.RuntimeInfo{Known: true, Stale: true, SyncedToGraph: true}, true},
		{"graph out of sync", lndclient.RuntimeInfo{Known: true, SyncedToChain: true}, true},
		{"healthy", lndclient.RuntimeInfo{Known: true, SyncedToChain: true, SyncedToGraph: true}, false},
	}
	for _, tc := range cases {
		if got := lndRuntimeShouldDeferNonessential(tc.info); got != tc.want {
			t.Errorf("%s: defer=%t, want %t", tc.name, got, tc.want)
		}
	}
}
