package server

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestFedimintComposeUsesExistingBitcoinAndLnd(t *testing.T) {
	paths := fedimintPaths{
		FedimintDataDir: "/var/lib/lightningos/apps-data/fedimint/fedimintd",
		GatewayDataDir:  "/var/lib/lightningos/apps-data/fedimint/gatewayd",
	}
	values := fedimintRuntimeValues{
		BitcoinRPCURL:         "http://bitcoind:8332",
		BitcoinRPCUser:        "bitcoin-user",
		BitcoinRPCPass:        "bitcoin-pass",
		UseBitcoinCoreNetwork: true,
		GatewayPasswordHash:   "$2a$10$abcdefghijklmnopqrstuuuuuuuuuuuuuuuuuuuuuuuuuuuuuu",
		LndRPCAddr:            "https://host.docker.internal:10009",
		LndTLSCertPath:        "/data/lnd/tls.cert",
		LndMacaroonPath:       "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon",
	}

	compose := fedimintComposeContents(paths, values)

	for _, want := range []string{
		"image: fedimint/fedimintd:v0.11.1",
		"image: fedimint/gatewayd:v0.11.1",
		"command: gatewayd lnd",
		"FM_BITCOIND_URL: ${FEDIMINT_BITCOIN_RPC_URL}",
		"FM_LND_RPC_ADDR: https://host.docker.internal:10009",
		"- /data/lnd:/data/lnd:ro",
		"name: bitcoincore_default",
	} {
		if !strings.Contains(compose, want) {
			t.Fatalf("compose missing %q\n%s", want, compose)
		}
	}
	if strings.Contains(compose, "bitcoind/bitcoin") {
		t.Fatalf("compose should not install a second bitcoind\n%s", compose)
	}
	if strings.Contains(compose, "10010:10010") || strings.Contains(compose, "FM_PORT_LDK") {
		t.Fatalf("LND mode should not expose the LDK lightning port\n%s", compose)
	}
	if strings.Contains(compose, "%!(") {
		t.Fatalf("compose has fmt artifact\n%s", compose)
	}
	if !strings.Contains(compose, "FM_GATEWAY_BCRYPT_PASSWORD_HASH: \"$$2a$$10$$") {
		t.Fatalf("bcrypt hash must escape dollars for compose interpolation\n%s", compose)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(compose), &parsed); err != nil {
		t.Fatalf("compose must be valid YAML: %v\n%s", err, compose)
	}
	if strings.Contains(compose, "\n/data/lnd/tls.cert\n") {
		t.Fatalf("compose must not render LND cert path as a standalone YAML line\n%s", compose)
	}
}

func TestFedimintComposeOmitsBitcoinCoreNetworkForExternalBitcoin(t *testing.T) {
	compose := fedimintComposeContents(fedimintPaths{}, fedimintRuntimeValues{
		BitcoinRPCURL:       "http://host.docker.internal:8332",
		BitcoinRPCUser:      "bitcoin-user",
		BitcoinRPCPass:      "bitcoin-pass",
		GatewayPasswordHash: "$2a$10$abcdefghijklmnopqrstuuuuuuuuuuuuuuuuuuuuuuuuuuuuuu",
		LndRPCAddr:          "https://host.docker.internal:10009",
		LndTLSCertPath:      "/data/lnd/tls.cert",
		LndMacaroonPath:     "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon",
	})

	if strings.Contains(compose, "bitcoincore_default") {
		t.Fatalf("compose should not reference bitcoincore network for external bitcoin\n%s", compose)
	}
}

func TestAddLndGrpcAccessOptionsPreservesExistingOptions(t *testing.T) {
	lines := []string{
		"[Application Options]",
		"tlsextraip=172.19.0.1",
		"tlsextradomain=host.docker.internal",
		"rpclisten=127.0.0.1:10009",
		"restlisten=172.19.0.1:8080",
		"# alias=LightningOS-Node",
	}

	got, changed := addLndGrpcAccessOptions(lines, []string{"172.20.0.1"})
	want := []string{
		"[Application Options]",
		"tlsextraip=172.20.0.1",
		"rpclisten=172.20.0.1:10009",
		"tlsextraip=172.19.0.1",
		"tlsextradomain=host.docker.internal",
		"rpclisten=127.0.0.1:10009",
		"restlisten=172.19.0.1:8080",
		"# alias=LightningOS-Node",
	}

	if !changed {
		t.Fatalf("expected LND config change")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected LND config\nwant: %#v\ngot:  %#v", want, got)
	}
}

func TestAddLndGrpcAccessOptionsNoChangeWhenPresent(t *testing.T) {
	lines := []string{
		"[Application Options]",
		"tlsextradomain=host.docker.internal",
		"rpclisten=127.0.0.1:10009",
		"tlsextraip=172.20.0.1",
		"rpclisten=172.20.0.1:10009",
	}

	got, changed := addLndGrpcAccessOptions(lines, []string{"172.20.0.1"})
	if changed {
		t.Fatalf("expected no change")
	}
	if !reflect.DeepEqual(got, lines) {
		t.Fatalf("unexpected LND config\nwant: %#v\ngot:  %#v", lines, got)
	}
}
