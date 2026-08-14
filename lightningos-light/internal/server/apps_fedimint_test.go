package server

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/lndclient"
	"lightningos-light/internal/system"

	"gopkg.in/yaml.v3"
)

func TestFedimintStartupRequiresStableRunningState(t *testing.T) {
	client := &cpuMinerPrivilegedClient{mode: "enforce", inspectStatus: "running"}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })
	if err := waitForFedimintAppStableWithPolicy(context.Background(), fedimintGuardianAppID, time.Millisecond, 3, 3); err != nil {
		t.Fatal(err)
	}
	if client.inspectCalls != 3 {
		t.Fatalf("stability checks = %d, want 3", client.inspectCalls)
	}
}

func TestFedimintStartupRejectsStoppedService(t *testing.T) {
	client := &cpuMinerPrivilegedClient{mode: "enforce", inspectStatus: "stopped"}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })
	err := waitForFedimintAppStableWithPolicy(context.Background(), fedimintGatewayAppID, time.Millisecond, 2, 3)
	if err == nil || !strings.Contains(err.Error(), "did not remain active") {
		t.Fatalf("expected unstable service error, got %v", err)
	}
}

func TestFedimintGuardianDeclarationUsesClosedHardenedRuntime(t *testing.T) {
	runtime := appmanifest.FedimintGuardianRuntime{Bitcoin: appmanifest.FedimintBitcoinRuntime{
		Mode: appmanifest.FedimintBitcoinModeApp, URL: "http://bitcoind:8332", User: "bitcoin-user", Pass: "pa'ss",
	}}
	compose, err := appmanifest.FedimintGuardianCompose(runtime)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		appmanifest.FedimintGuardianImage,
		`user: "1000:1000"`,
		"read_only: true",
		"cap_drop:\n      - ALL",
		"no-new-privileges:true",
		"FM_ENABLE_IROH: \"true\"",
		"FM_BITCOIND_PASSWORD: 'pa''ss'",
		"- bitcoincore",
		"name: bitcoincore_default",
		`- "8173:8173/tcp"`,
		`- "8173:8173/udp"`,
		`- "8174:8174/udp"`,
		`- "8175:8175/tcp"`,
	} {
		if !strings.Contains(compose, want) {
			t.Fatalf("Guardian declaration missing %q\n%s", want, compose)
		}
	}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(compose), &parsed); err != nil {
		t.Fatalf("Guardian declaration must be valid YAML: %v", err)
	}
}

func TestFedimintGatewayDeclarationUsesDedicatedLNDMaterial(t *testing.T) {
	runtime := appmanifest.FedimintGatewayRuntime{
		Bitcoin: appmanifest.FedimintBitcoinRuntime{
			Mode: appmanifest.FedimintBitcoinModeRemote, URL: "http://bitcoin.example:8332", User: "bitcoin-user", Pass: "bitcoin-pass",
		},
		GatewayPasswordHash: "$2a$10$abcdefghijklmnopqrstuuuuuuuuuuuuuuuuuuuuuuuuuuuuuu",
	}
	compose, err := appmanifest.FedimintGatewayCompose(runtime)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		appmanifest.FedimintGatewayImage,
		`user: "1000:1000"`,
		"read_only: true",
		"cap_drop:\n      - ALL",
		"no-new-privileges:true",
		`command: ["gatewayd", "lnd"]`,
		"FM_LND_TLS_CERT: /run/lnd/tls.cert",
		"FM_LND_MACAROON: /run/lnd/fedimint-gateway.macaroon",
		"${FEDIMINT_GATEWAY_CREDENTIAL_ROOT}:/run/lnd:ro",
		`- "8176:8176/tcp"`,
		`- "8177:8177/udp"`,
	} {
		if !strings.Contains(compose, want) {
			t.Fatalf("Gateway declaration missing %q\n%s", want, compose)
		}
	}
	for _, forbidden := range []string{
		"admin.macaroon",
		"/data/lnd:/data/lnd",
		"network_mode: host",
		"privileged: true",
	} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("Gateway declaration must not contain %q\n%s", forbidden, compose)
		}
	}
	if !strings.Contains(compose, `FM_GATEWAY_BCRYPT_PASSWORD_HASH: "$$2a$$10$$`) {
		t.Fatalf("Gateway bcrypt hash must be escaped for Compose\n%s", compose)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(compose), &parsed); err != nil {
		t.Fatalf("Gateway declaration must be valid YAML: %v", err)
	}
}

func TestFedimintGatewayMacaroonPermissionsMatchUpstreamRPCUse(t *testing.T) {
	want := []lndclient.MacaroonPermission{
		{Entity: "info", Action: "read"},
		{Entity: "invoices", Action: "read"},
		{Entity: "invoices", Action: "write"},
		{Entity: "offchain", Action: "read"},
		{Entity: "offchain", Action: "write"},
		{Entity: "onchain", Action: "read"},
		{Entity: "onchain", Action: "write"},
		{Entity: "peers", Action: "read"},
		{Entity: "peers", Action: "write"},
	}
	if got := fedimintGatewayMacaroonPermissions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected Gateway permissions: got %#v want %#v", got, want)
	}
	for _, permission := range fedimintGatewayMacaroonPermissions() {
		if permission.Entity == "macaroon" || permission.Entity == "signer" || permission.Entity == "message" {
			t.Fatalf("Gateway received unrelated LND permission: %#v", permission)
		}
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
