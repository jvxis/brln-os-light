package server

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"lightningos-light/internal/appmanifest"
)

func TestFedimintExternalBitcoinUsesFixedConsumerBoundary(t *testing.T) {
	values := fedimintBitcoinBackendFromConfig(bitcoinRPCConfig{
		Host: "127.0.0.1:18443",
		User: "rpc-user",
		Pass: "rpc-pass",
	})
	if values.URL != "http://"+appmanifest.BitcoinConsumerHostGateway+":18443" ||
		!values.UseBitcoinCoreNetwork || !values.NeedsLocalRPCBridgeUFW || values.LocalExternalBitcoinPort != 18443 {
		t.Fatalf("unexpected external Bitcoin wiring: %#v", values)
	}
}

func TestFedimintGuardianComposeUsesIrohAndBitcoind(t *testing.T) {
	paths := fedimintGuardianPaths{
		DataDir: "/var/lib/lightningos/apps-data/fedimint-guardian/fedimintd",
	}
	values := fedimintBitcoinBackendValues{
		URL:  "http://bitcoin.br-ln.com:8085",
		User: "bitcoin-user",
		Pass: "bitcoin-pass",
	}

	compose := fedimintGuardianComposeContents(paths, values)

	for _, want := range []string{
		"image: fedimint/fedimintd:v0.11.1",
		"FM_ENABLE_IROH: \"true\"",
		"FM_BITCOIND_URL: 'http://bitcoin.br-ln.com:8085'",
		"FM_BITCOIND_USERNAME: 'bitcoin-user'",
		"FM_BITCOIND_PASSWORD: 'bitcoin-pass'",
		"- \"8173:8173/tcp\"",
		"- \"8173:8173/udp\"",
		"- \"8174:8174/udp\"",
		"- \"8175:8175/tcp\"",
	} {
		if !strings.Contains(compose, want) {
			t.Fatalf("guardian compose missing %q\n%s", want, compose)
		}
	}
	for _, unwanted := range []string{
		"FM_ESPLORA_URL",
		"FM_LND_RPC_ADDR",
		"gatewayd",
		"10010:10010",
	} {
		if strings.Contains(compose, unwanted) {
			t.Fatalf("guardian compose should not contain %q\n%s", unwanted, compose)
		}
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(compose), &parsed); err != nil {
		t.Fatalf("guardian compose must be valid YAML: %v\n%s", err, compose)
	}
}

func TestFedimintGuardianComposeCanJoinBitcoinCoreNetwork(t *testing.T) {
	paths := fedimintGuardianPaths{
		DataDir: "/var/lib/lightningos/apps-data/fedimint-guardian/fedimintd",
	}
	values := fedimintBitcoinBackendValues{
		URL:                   "http://bitcoind:8332",
		User:                  "lightningos",
		Pass:                  "pa'ss",
		UseBitcoinCoreNetwork: true,
	}

	compose := fedimintGuardianComposeContents(paths, values)

	for _, want := range []string{
		"FM_BITCOIND_URL: 'http://bitcoind:8332'",
		"FM_BITCOIND_PASSWORD: 'pa''ss'",
		"- bitcoincore",
		"name: bitcoincore_default",
	} {
		if !strings.Contains(compose, want) {
			t.Fatalf("guardian compose missing %q\n%s", want, compose)
		}
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(compose), &parsed); err != nil {
		t.Fatalf("guardian compose must be valid YAML: %v\n%s", err, compose)
	}
}

func TestFedimintGatewayComposeUsesLndIrohAndBitcoind(t *testing.T) {
	paths := fedimintGatewayPaths{
		DataDir: "/var/lib/lightningos/apps-data/fedimint-gateway/gatewayd",
	}
	values := fedimintGatewayRuntimeValues{
		GatewayPasswordHash: "$2a$10$abcdefghijklmnopqrstuuuuuuuuuuuuuuuuuuuuuuuuuuuuuu",
		LndRPCAddr:          "https://host.docker.internal:10009",
		LndTLSCertPath:      "/data/lnd/tls.cert",
		LndMacaroonPath:     "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon",
		BitcoinBackend: fedimintBitcoinBackendValues{
			URL:  "http://bitcoin.br-ln.com:8085",
			User: "bitcoin-user",
			Pass: "bitcoin-pass",
		},
	}

	compose := fedimintGatewayComposeContents(paths, values)

	for _, want := range []string{
		"image: fedimint/gatewayd:v0.11.1",
		"command: gatewayd lnd",
		"FM_GATEWAY_IROH_LISTEN_ADDR: 0.0.0.0:8177",
		"FM_BITCOIND_URL: 'http://bitcoin.br-ln.com:8085'",
		"FM_BITCOIND_USERNAME: 'bitcoin-user'",
		"FM_BITCOIND_PASSWORD: 'bitcoin-pass'",
		"FM_LND_RPC_ADDR: https://host.docker.internal:10009",
		"FM_LND_TLS_CERT: /data/lnd/tls.cert",
		"FM_LND_MACAROON: /data/lnd/data/chain/bitcoin/mainnet/admin.macaroon",
		"- /data/lnd:/data/lnd:ro",
		"- \"8176:8176/tcp\"",
		"- \"8177:8177/udp\"",
	} {
		if !strings.Contains(compose, want) {
			t.Fatalf("gateway compose missing %q\n%s", want, compose)
		}
	}
	for _, unwanted := range []string{
		"fedimintd",
		"FM_ESPLORA_URL",
		"FM_PORT_LDK",
		"10010:10010",
	} {
		if strings.Contains(compose, unwanted) {
			t.Fatalf("gateway compose should not contain %q\n%s", unwanted, compose)
		}
	}
	if strings.Contains(compose, "%!(") {
		t.Fatalf("gateway compose has fmt artifact\n%s", compose)
	}
	if !strings.Contains(compose, "FM_GATEWAY_BCRYPT_PASSWORD_HASH: \"$$2a$$10$$") {
		t.Fatalf("bcrypt hash must escape dollars for compose interpolation\n%s", compose)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(compose), &parsed); err != nil {
		t.Fatalf("gateway compose must be valid YAML: %v\n%s", err, compose)
	}
	if strings.Contains(compose, "\n/data/lnd/tls.cert\n") {
		t.Fatalf("compose must not render LND cert path as a standalone YAML line\n%s", compose)
	}
}

func TestFedimintGatewayComposeCanJoinBitcoinCoreNetwork(t *testing.T) {
	paths := fedimintGatewayPaths{
		DataDir: "/var/lib/lightningos/apps-data/fedimint-gateway/gatewayd",
	}
	values := fedimintGatewayRuntimeValues{
		GatewayPasswordHash: "$2a$10$abcdefghijklmnopqrstuuuuuuuuuuuuuuuuuuuuuuuuuuuuuu",
		LndRPCAddr:          "https://host.docker.internal:10009",
		LndTLSCertPath:      "/data/lnd/tls.cert",
		LndMacaroonPath:     "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon",
		BitcoinBackend: fedimintBitcoinBackendValues{
			URL:                   "http://bitcoind:8332",
			User:                  "lightningos",
			Pass:                  "pa'ss",
			UseBitcoinCoreNetwork: true,
		},
	}

	compose := fedimintGatewayComposeContents(paths, values)

	for _, want := range []string{
		"FM_BITCOIND_URL: 'http://bitcoind:8332'",
		"FM_BITCOIND_PASSWORD: 'pa''ss'",
		"- bitcoincore",
		"name: bitcoincore_default",
	} {
		if !strings.Contains(compose, want) {
			t.Fatalf("gateway compose missing %q\n%s", want, compose)
		}
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(compose), &parsed); err != nil {
		t.Fatalf("gateway compose must be valid YAML: %v\n%s", err, compose)
	}
}

func TestFedimintGatewayStartupErrorExplainsRestrictedBitcoinRPC(t *testing.T) {
	err := fedimintGatewayStartupError(errors.New("container exited"))
	if err == nil || !strings.Contains(err.Error(), "failed startup validation") {
		t.Fatalf("expected generic startup validation error, got %v", err)
	}

	err = fedimintGatewayStartupError(errors.New("JSON-RPC error: Method not found"))
	if err == nil || !strings.Contains(err.Error(), "createwallet") {
		t.Fatalf("expected wallet RPC requirement, got %v", err)
	}
}

func TestAddLndGrpcAccessOptionsPreservesExistingOptions(t *testing.T) {
	lines := []string{
		"[Application Options]",
		"tlsextraip=192.168.68.64",
		"tlsextradomain=host.docker.internal",
		"rpclisten=127.0.0.1:10009",
		"restlisten=172.19.0.1:8080",
		"# alias=LightningOS-Node",
	}

	got, changed := addLndGrpcAccessOptions(lines, []string{"172.17.0.1"})
	want := []string{
		"[Application Options]",
		"tlsextraip=172.17.0.1",
		"rpclisten=172.17.0.1:10009",
		"tlsextraip=192.168.68.64",
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

func TestAddLndGrpcAccessOptionsRemovesStaleDockerBridgeListeners(t *testing.T) {
	lines := []string{
		"[Application Options]",
		"tlsextraip=172.19.0.1",
		"rpclisten=172.19.0.1:10009",
		"rpclisten=127.0.0.1:10009",
		"alias=LightningOS-Node",
	}

	got, changed := addLndGrpcAccessOptions(lines, []string{"172.17.0.1"})
	want := []string{
		"[Application Options]",
		"tlsextradomain=host.docker.internal",
		"tlsextraip=172.17.0.1",
		"rpclisten=172.17.0.1:10009",
		"rpclisten=127.0.0.1:10009",
		"alias=LightningOS-Node",
	}

	if !changed {
		t.Fatalf("expected stale Docker listener cleanup")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected LND config\nwant: %#v\ngot:  %#v", want, got)
	}
}
