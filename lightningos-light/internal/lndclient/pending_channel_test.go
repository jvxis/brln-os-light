package lndclient

import (
	"testing"

	"lightningos-light/lnrpc"
)

func TestPendingChannelInitiator(t *testing.T) {
	tests := []struct {
		value lnrpc.Initiator
		want  string
	}{
		{value: lnrpc.Initiator_INITIATOR_LOCAL, want: "local"},
		{value: lnrpc.Initiator_INITIATOR_REMOTE, want: "remote"},
		{value: lnrpc.Initiator_INITIATOR_BOTH, want: "both"},
		{value: lnrpc.Initiator_INITIATOR_UNKNOWN, want: "unknown"},
	}
	for _, test := range tests {
		if got := pendingChannelInitiator(test.value); got != test.want {
			t.Fatalf("pendingChannelInitiator(%v) = %q, want %q", test.value, got, test.want)
		}
	}
}
