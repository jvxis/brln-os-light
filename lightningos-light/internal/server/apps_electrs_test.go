package server

import (
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func TestElectrsExternalBitcoinUsesFixedConsumerBoundary(t *testing.T) {
	values := electrsRuntimeValues{
		BitcoinRPCHost: appmanifest.BitcoinConsumerHostGateway,
		BitcoinRPCPort: 18443,
		BitcoinP2PHost: appmanifest.BitcoinConsumerHostGateway,
		BitcoinP2PPort: 18444,
		Network:        "regtest",
		BitcoinMode:    appmanifest.ElectrsBitcoinModeNative,
	}
	compose := electrsComposeContents(electrsPaths{}, values)
	for _, want := range []string{
		"--daemon-rpc-addr=172.31.253.1:18443",
		"--daemon-p2p-addr=172.31.253.1:18444",
		"--network=regtest",
		"name: bitcoincore_default",
	} {
		if !strings.Contains(compose, want) {
			t.Fatalf("external compose missing %q\n%s", want, compose)
		}
	}
}

func TestElectrsNetworkAndP2PPort(t *testing.T) {
	for _, test := range []struct {
		chain   string
		network string
		port    int
	}{
		{chain: "main", network: "bitcoin", port: 8333},
		{chain: "regtest", network: "regtest", port: 18444},
		{chain: "signet", network: "signet", port: 38333},
		{chain: "test", network: "testnet", port: 18333},
	} {
		network, port := electrsNetworkAndP2PPort(test.chain)
		if network != test.network || port != test.port {
			t.Fatalf("chain %q => %s/%d, want %s/%d", test.chain, network, port, test.network, test.port)
		}
	}
}

func TestParseElectrsIndexHeight(t *testing.T) {
	sample := `# HELP electrs_index_height Indexed block height
# TYPE electrs_index_height gauge
electrs_index_height{type="tip"} 892044
electrs_mempool_count 12345
`
	h, err := parseElectrsIndexHeight(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h != 892044 {
		t.Fatalf("expected 892044, got %d", h)
	}
}

func TestParseElectrsIndexHeightMissingLabel(t *testing.T) {
	sample := `electrs_index_height{type="other"} 42
`
	if _, err := parseElectrsIndexHeight(strings.NewReader(sample)); err == nil {
		t.Fatal("expected error when type=\"tip\" is absent")
	}
}
