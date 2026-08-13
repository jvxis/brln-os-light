package server

import "testing"

func TestPublicPoolExternalZMQHostRejectsLoopback(t *testing.T) {
	for _, endpoint := range []string{"tcp://127.0.0.1:28332", "tcp://[::1]:28332", "tcp://0.0.0.0:28332", "localhost:28332"} {
		if got := publicPoolExternalZMQHost(endpoint); got != "" {
			t.Fatalf("expected %q to be rejected, got %q", endpoint, got)
		}
	}
	if got := publicPoolExternalZMQHost("tcp://192.168.1.20:28332"); got != "tcp://192.168.1.20:28332" {
		t.Fatalf("unexpected external endpoint: %q", got)
	}
}

func TestPublicPoolHTTPRPCURLFormatsIPv6(t *testing.T) {
	if got := toHTTPRPCURL("2001:db8::1"); got != "http://[2001:db8::1]" {
		t.Fatalf("unexpected URL: %q", got)
	}
}
