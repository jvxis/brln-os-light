package appmanifest

import (
	"strings"
	"testing"
)

func TestElectrsCatalogPinsVerifiedSourceAndNonRootImage(t *testing.T) {
	if ElectrsRelease != "0.11.1" || ElectrsSourceCommit != "35216c6d30148be8e6763d913d437330f431fc03" {
		t.Fatalf("unexpected Electrs source identity: %s/%s", ElectrsRelease, ElectrsSourceCommit)
	}
	if len(ElectrsSourceSHA256) != 64 || !strings.Contains(ElectrsSourceURL, ElectrsSourceTag) {
		t.Fatalf("Electrs source is not closed: %s/%s", ElectrsSourceURL, ElectrsSourceSHA256)
	}
	dockerfile := ElectrsDockerfile()
	for _, required := range []string{ElectrsBaseImage, ElectrsSourceDir, "ca-certificates", "cargo install --locked", "USER 1000:1000"} {
		if !strings.Contains(dockerfile, required) {
			t.Fatalf("Dockerfile missing %q", required)
		}
	}
	for _, forbidden := range []string{"git clone", "latest", "curl |", "USER root"} {
		if strings.Contains(dockerfile, forbidden) {
			t.Fatalf("Dockerfile contains mutable or privileged input %q", forbidden)
		}
	}
}

func TestElectrsRuntimeEnvIsCanonicalAndClosed(t *testing.T) {
	for _, runtime := range []ElectrsRuntime{
		{BitcoinMode: ElectrsBitcoinModeApp, Network: "bitcoin"},
		{BitcoinMode: ElectrsBitcoinModeNative, Network: "regtest"},
	} {
		raw, err := ElectrsRuntimeEnv(runtime)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := ParseElectrsRuntimeEnv([]byte(raw))
		if err != nil || parsed != runtime {
			t.Fatalf("parsed/error=%#v/%v", parsed, err)
		}
	}
	for _, raw := range []string{
		"ELECTRS_BITCOIN_MODE=remote\nELECTRS_NETWORK=bitcoin\n",
		"ELECTRS_BITCOIN_MODE=app\nELECTRS_NETWORK=mainnet\n",
		"ELECTRS_BITCOIN_MODE=app\nELECTRS_NETWORK=bitcoin\nRPC_HOST=attacker\n",
		"ELECTRS_NETWORK=bitcoin\nELECTRS_BITCOIN_MODE=app\n",
		"ELECTRS_BITCOIN_MODE=app\r\nELECTRS_NETWORK=bitcoin\r\n",
	} {
		if _, err := ParseElectrsRuntimeEnv([]byte(raw)); err == nil {
			t.Fatalf("expected environment to be rejected: %q", raw)
		}
	}
}

func TestElectrsComposeClosesPrivilegesAndBitcoinWiring(t *testing.T) {
	for _, test := range []struct {
		runtime ElectrsRuntime
		host    string
		rpcPort string
	}{
		{runtime: ElectrsRuntime{BitcoinMode: ElectrsBitcoinModeApp, Network: "bitcoin"}, host: "bitcoind", rpcPort: "8332"},
		{runtime: ElectrsRuntime{BitcoinMode: ElectrsBitcoinModeNative, Network: "regtest"}, host: BitcoinConsumerHostGateway, rpcPort: "18443"},
	} {
		compose, err := ElectrsCompose(test.runtime)
		if err != nil {
			t.Fatal(err)
		}
		for _, required := range []string{
			"image: " + ElectrsImage,
			"user: \"1000:1000\"",
			"ulimits:\n      nofile:\n        soft: 65536\n        hard: 65536",
			"read_only: true",
			"no-new-privileges:true",
			"127.0.0.1:50001:50001",
			"127.0.0.1:4224:4224",
			"--daemon-rpc-addr=" + test.host + ":" + test.rpcPort,
			"./" + ElectrsCookieFile + ":/run/bitcoin.cookie:ro",
		} {
			if !strings.Contains(compose, required) {
				t.Fatalf("compose missing %q:\n%s", required, compose)
			}
		}
		for _, forbidden := range []string{"privileged: true", "network_mode: host", "/var/run/docker.sock", "/data/bitcoin", "0.0.0.0:50001:50001"} {
			if strings.Contains(compose, forbidden) {
				t.Fatalf("compose contains forbidden %q", forbidden)
			}
		}
	}
}

func TestElectrsComposeUsesEnrolledDataDirectory(t *testing.T) {
	runtime := ElectrsRuntime{BitcoinMode: ElectrsBitcoinModeApp, Network: "bitcoin", DataDir: "/mnt/chain/lightningos/electrs"}
	compose, err := ElectrsCompose(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compose, "/mnt/chain/lightningos/electrs/db:/data/db") || strings.Contains(compose, "name: "+ElectrsVolume) {
		t.Fatalf("Electrs external storage is not a closed bind mount:\n%s", compose)
	}
}

func TestElectrsNetworkContract(t *testing.T) {
	tests := map[string]ElectrsNetwork{
		"bitcoin": {ElectrsName: "bitcoin", BitcoinName: "main", RPCPort: 8332, P2PPort: 8333},
		"testnet": {ElectrsName: "testnet", BitcoinName: "test", RPCPort: 18332, P2PPort: 18333},
		"signet":  {ElectrsName: "signet", BitcoinName: "signet", RPCPort: 38332, P2PPort: 38333},
		"regtest": {ElectrsName: "regtest", BitcoinName: "regtest", RPCPort: 18443, P2PPort: 18444},
	}
	for name, want := range tests {
		got, err := ElectrsNetworkForName(name)
		if err != nil || got != want {
			t.Fatalf("%s network/error=%#v/%v want=%#v", name, got, err, want)
		}
	}
	if _, err := ElectrsNetworkForName("mainnet;reboot"); err == nil {
		t.Fatal("expected unknown network rejection")
	}
}
