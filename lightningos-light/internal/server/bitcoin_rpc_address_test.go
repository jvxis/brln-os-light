package server

import "testing"

func TestBitcoinRPCHostPortNormalizesSchemesAndDuplicatePorts(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "host with port", value: "127.0.0.1:8332", want: "127.0.0.1:8332"},
		{name: "http url", value: "http://127.0.0.1:8332", want: "127.0.0.1:8332"},
		{name: "tcp url", value: "tcp://127.0.0.1:18443", want: "127.0.0.1:18443"},
		{name: "host without port", value: "localhost", want: "localhost:8332"},
		{name: "duplicated port", value: "http://127.0.0.1:8332:8332", want: "127.0.0.1:8332"},
		{name: "duplicate with path", value: "https://localhost:8332:8332/wallet/test?timeout=5", want: "localhost:8332"},
		{name: "ipv6 url", value: "http://[::1]:8332", want: "[::1]:8332"},
		{name: "ipv6 duplicate", value: "http://[::1]:8332:8332", want: "[::1]:8332"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := bitcoinRPCHostPort(tc.value, 8332); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestDuplicateBitcoinRPCPortRepairLeavesOtherURLsUntouched(t *testing.T) {
	for _, value := range []string{
		"http://127.0.0.1:8332",
		"http://[::1]:8332",
		"http://127.0.0.1:8332:18443",
		"http://127.0.0.1:99999:99999",
		"http://127.0.0.1:8332/wallet/8332:8332?rpc=8332:8332",
	} {
		if got := repairDuplicateBitcoinRPCURLPort(value); got != value {
			t.Errorf("unrelated URL changed: %q => %q", value, got)
		}
	}
}
