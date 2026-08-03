package server

import (
	"testing"

	"lightningos-light/internal/lndclient"
)

func TestLNDRuntimeShouldDeferNonessential(t *testing.T) {
	tests := []struct {
		name string
		info lndclient.RuntimeInfo
		want bool
	}{
		{name: "unknown", info: lndclient.RuntimeInfo{}, want: true},
		{name: "stale", info: lndclient.RuntimeInfo{Known: true, Stale: true, SyncedToGraph: true}, want: true},
		{name: "graph syncing", info: lndclient.RuntimeInfo{Known: true}, want: true},
		{name: "ready", info: lndclient.RuntimeInfo{Known: true, SyncedToGraph: true}, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := lndRuntimeShouldDeferNonessential(test.info); got != test.want {
				t.Fatalf("lndRuntimeShouldDeferNonessential() = %t, want %t", got, test.want)
			}
		})
	}
}
