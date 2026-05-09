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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := bitcoinRPCHostPort(tc.value, 8332); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}
