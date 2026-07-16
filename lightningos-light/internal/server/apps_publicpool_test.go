package server

import (
	"os"
	"path/filepath"
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

func TestPublicPoolDockerZMQHost(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		host     string
		want     string
	}{
		{name: "container loopback", endpoint: "tcp://127.0.0.1:28332", host: "bitcoind", want: "tcp://bitcoind:28332"},
		{name: "container unspecified", endpoint: "tcp://0.0.0.0:28332", host: "bitcoind", want: "tcp://bitcoind:28332"},
		{name: "remote", endpoint: "bitcoin.example:28332", host: "bitcoind", want: "tcp://bitcoin.example:28332"},
		{name: "ipv6", endpoint: "tcp://[2001:db8::1]:28332", host: "bitcoind", want: "tcp://[2001:db8::1]:28332"},
		{name: "invalid", endpoint: "tcp://bitcoin.example", host: "bitcoind", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := publicPoolDockerZMQHost(tc.endpoint, tc.host); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

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

func TestEnsurePublicPoolEnvSetsAndClearsZMQ(t *testing.T) {
	dir := t.TempDir()
	paths := publicPoolPaths{EnvPath: filepath.Join(dir, ".env")}
	values := publicPoolRuntimeValues{
		BitcoinRPCURL:  "http://bitcoin.example",
		BitcoinRPCPort: 8332,
		BitcoinRPCUser: "user",
		BitcoinRPCPass: "pass",
		BitcoinZMQHost: "tcp://bitcoin.example:28332",
	}
	if err := ensurePublicPoolEnv(paths, values); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(paths.EnvPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "BITCOIN_ZMQ_HOST=tcp://bitcoin.example:28332") {
		t.Fatalf("ZMQ endpoint missing from env: %s", raw)
	}

	values.BitcoinZMQHost = ""
	if err := ensurePublicPoolEnv(paths, values); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(paths.EnvPath)
	if err != nil {
		t.Fatal(err)
	}
	exists, value, err := envValueState(paths.EnvPath, "BITCOIN_ZMQ_HOST")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || value != "" {
		t.Fatalf("stale ZMQ endpoint was not cleared: %s", raw)
	}
}
