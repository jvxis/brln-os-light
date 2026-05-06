package server

import "testing"

func TestParseElementsLocalBitcoinRPCConfigFromLNDConfPrefersActiveLocal(t *testing.T) {
	raw := `[Bitcoind]
bitcoind.rpchost=127.0.0.1:18443
bitcoind.rpcuser=active-user
bitcoind.rpcpass=active-pass

# LightningOS Bitcoin Local
# bitcoind.rpchost=127.0.0.1:8332
# bitcoind.rpcuser=tagged-user
# bitcoind.rpcpass=tagged-pass
`

	cfg, ok := parseElementsLocalBitcoinRPCConfigFromLNDConf(raw)
	if !ok {
		t.Fatalf("expected local config")
	}
	if cfg.Host != "127.0.0.1:18443" || cfg.User != "active-user" || cfg.Pass != "active-pass" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestParseElementsLocalBitcoinRPCConfigFromLNDConfFallsBackToTaggedLocal(t *testing.T) {
	raw := `[Bitcoind]
# LightningOS Bitcoin Remote
bitcoind.rpchost=bitcoin.br-ln.com:8085
bitcoind.rpcuser=remote-user
bitcoind.rpcpass=remote-pass

# LightningOS Bitcoin Local
# bitcoind.rpchost=127.0.0.1:8333
# bitcoind.rpcuser=local-user
# bitcoind.rpcpass=local-pass
# bitcoind.zmqpubrawblock=tcp://127.0.0.1:28334
# bitcoind.zmqpubrawtx=tcp://127.0.0.1:28335
`

	cfg, ok := parseElementsLocalBitcoinRPCConfigFromLNDConf(raw)
	if !ok {
		t.Fatalf("expected tagged local config")
	}
	if cfg.Host != "127.0.0.1:8333" || cfg.User != "local-user" || cfg.Pass != "local-pass" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.ZMQBlock != "tcp://127.0.0.1:28334" || cfg.ZMQTx != "tcp://127.0.0.1:28335" {
		t.Fatalf("unexpected zmq config: %+v", cfg)
	}
}

func TestParseElementsLocalBitcoinRPCConfigFromLNDConfRejectsMissingCredentials(t *testing.T) {
	raw := `[Bitcoind]
bitcoind.rpchost=127.0.0.1:8332
bitcoind.rpcuser=local-user
`

	if cfg, ok := parseElementsLocalBitcoinRPCConfigFromLNDConf(raw); ok {
		t.Fatalf("expected missing password to be rejected, got %+v", cfg)
	}
}

func TestLocalBitcoinConfigCandidatesIncludesAdminBitcoinConf(t *testing.T) {
	paths := bitcoinCorePaths{ConfigPath: "/data/bitcoin/bitcoin.conf"}
	candidates := localBitcoinConfigCandidates(paths)
	if !stringInSlice("/home/admin/.bitcoin/bitcoin.conf", candidates) {
		t.Fatalf("expected /home/admin/.bitcoin/bitcoin.conf in candidates: %#v", candidates)
	}
}

func TestNormalizeElementsDataDir(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "blank defaults", value: "", want: elementsDefaultDataDir},
		{name: "cleans path", value: "/mnt/liquid/../liquid/elements/", want: "/mnt/liquid/elements"},
		{name: "rejects relative", value: "mnt/liquid/elements", wantErr: true},
		{name: "rejects root", value: "/", wantErr: true},
		{name: "rejects system dir", value: "/var/lib/elements", wantErr: true},
		{name: "rejects bitcoin dir", value: "/data/bitcoin/elements", wantErr: true},
		{name: "rejects spaces", value: "/mnt/liquid ssd/elements", wantErr: true},
		{name: "rejects shell chars", value: "/mnt/liquid;ssd/elements", wantErr: true},
		{name: "allows data elements default", value: "/data/elements", want: "/data/elements"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeElementsDataDir(tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}
