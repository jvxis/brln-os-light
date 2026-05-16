package server

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestFedimintGuardianComposeUsesIrohAndEsplora(t *testing.T) {
	paths := fedimintGuardianPaths{
		DataDir: "/var/lib/lightningos/apps-data/fedimint-guardian/fedimintd",
	}

	compose := fedimintGuardianComposeContents(paths)

	for _, want := range []string{
		"image: fedimint/fedimintd:v0.11.1",
		"FM_ENABLE_IROH: \"true\"",
		"FM_ESPLORA_URL: https://mempool.space/api",
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
		"FM_BITCOIND_URL",
		"FM_BITCOIND_USERNAME",
		"FM_BITCOIND_PASSWORD",
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

func TestFedimintGatewayComposeUsesLndIrohAndEsplora(t *testing.T) {
	paths := fedimintGatewayPaths{
		DataDir: "/var/lib/lightningos/apps-data/fedimint-gateway/gatewayd",
	}
	values := fedimintGatewayRuntimeValues{
		GatewayPasswordHash: "$2a$10$abcdefghijklmnopqrstuuuuuuuuuuuuuuuuuuuuuuuuuuuuuu",
		LndRPCAddr:          "https://host.docker.internal:10009",
		LndTLSCertPath:      "/data/lnd/tls.cert",
		LndMacaroonPath:     "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon",
	}

	compose := fedimintGatewayComposeContents(paths, values)

	for _, want := range []string{
		"image: fedimint/gatewayd:v0.11.1",
		"command: gatewayd lnd",
		"FM_GATEWAY_IROH_LISTEN_ADDR: 0.0.0.0:8177",
		"FM_ESPLORA_URL: https://mempool.space/api",
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
		"FM_BITCOIND_URL",
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
