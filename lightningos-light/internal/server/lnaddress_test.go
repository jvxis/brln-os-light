package server

import "testing"

func TestValidateLNURLPayDescriptor(t *testing.T) {
	valid := lnurlPayResponse{MinSendable: 1_000, MaxSendable: 100_000_000}
	if err := validateLNURLPayDescriptor(valid); err != nil {
		t.Fatalf("valid descriptor rejected: %v", err)
	}

	for _, candidate := range []lnurlPayResponse{
		{MinSendable: 0, MaxSendable: 100_000},
		{MinSendable: 1_000, MaxSendable: 0},
		{MinSendable: 2_000, MaxSendable: 1_000},
	} {
		if err := validateLNURLPayDescriptor(candidate); err == nil {
			t.Fatalf("invalid descriptor accepted: %#v", candidate)
		}
	}
}
