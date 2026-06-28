package server

import (
	"strings"
	"testing"
)

func TestPublicPoolDockerRPCURLUsesHostGatewayForLocalBind(t *testing.T) {
	tests := []struct {
		name string
		host string
		want string
	}{
		{name: "empty", host: "", want: "http://host.docker.internal"},
		{name: "localhost", host: "localhost", want: "http://host.docker.internal"},
		{name: "ipv4 loopback", host: "127.0.0.1", want: "http://host.docker.internal"},
		{name: "ipv6 loopback", host: "::1", want: "http://host.docker.internal"},
		{name: "unspecified ipv4", host: "0.0.0.0", want: "http://host.docker.internal"},
		{name: "lan ip", host: "192.168.1.100", want: "http://192.168.1.100"},
		{name: "dns", host: "bitcoin.lan", want: "http://bitcoin.lan"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := publicPoolDockerRPCURL(tc.host); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestPublicPoolUfwCommandForBridge(t *testing.T) {
	if got := publicPoolUfwCommandForBridge("br-abcdef123456", 8332); got != "sudo ufw allow in on br-abcdef123456 to any port 8332 proto tcp" {
		t.Fatalf("unexpected concrete command: %q", got)
	}

	fallback := publicPoolUfwCommandForBridge("", 0)
	if want := "publicpool_default"; !strings.Contains(fallback, want) {
		t.Fatalf("expected fallback command to inspect %q, got %q", want, fallback)
	}
	if want := "to any port 8332 proto tcp"; !strings.Contains(fallback, want) {
		t.Fatalf("expected fallback command to open default RPC port, got %q", fallback)
	}
}
