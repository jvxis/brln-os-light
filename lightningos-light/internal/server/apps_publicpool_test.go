package server

import "testing"

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
