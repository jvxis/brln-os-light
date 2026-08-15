package appmanifest

import (
	"strings"
	"testing"
)

func TestTapdCatalogUsesOfficialImmutableImage(t *testing.T) {
	image, err := CatalogImageForVariant(TapdID, TapdImageApp)
	if err != nil {
		t.Fatal(err)
	}
	if image != TapdImage || !strings.Contains(image, "lightninglabs/taproot-assets:v0.8.0@sha256:") {
		t.Fatalf("unexpected tapd image: %q", image)
	}
	if _, err := CatalogImageForVariant(TapdID, "latest"); err == nil {
		t.Fatal("mutable tapd image variant was accepted")
	}
}

func TestTapdComposeIsConstrained(t *testing.T) {
	paths := TapdComposePaths{
		DataDir: "/var/lib/lightningos/apps-data/tapd/data", ConfigPath: "/snapshot/tapd.conf",
		TLSCertPath: "/snapshot/lnd/tls.cert", MacaroonPath: "/snapshot/lnd/tapd.macaroon",
	}
	compose := TapdCompose(paths)
	for _, required := range []string{TapdImage, "read_only: true", "cap_drop:\n      - ALL", "no-new-privileges:true", "network_mode: host"} {
		if !strings.Contains(compose, required) {
			t.Fatalf("compose missing %q", required)
		}
	}
	for _, forbidden := range []string{"/data/lnd:/root/.lnd", "admin.macaroon", "/var/run/docker.sock", "ports:"} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("compose contains forbidden value %q", forbidden)
		}
	}
}

func TestTapdConfigRoundTrip(t *testing.T) {
	config, err := TapdConfig("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	password, err := ParseTapdConfig([]byte(config))
	if err != nil || password != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("round trip failed: password=%q err=%v", password, err)
	}
	if strings.Contains(config, "admin.macaroon") || strings.Contains(config, "/root/.lnd") {
		t.Fatal("tapd config uses broad or administrative LND material")
	}
}

func TestTapdCLIIsTyped(t *testing.T) {
	assetID := strings.Repeat("ab", 32)
	args, err := TapdCLIArgs(TapdCLIRequest{Command: TapdCLIAddressNew, AssetID: assetID, Amount: 42})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(args, " "); got != "addrs new --asset_id "+assetID+" --amt 42" {
		t.Fatalf("unexpected argv: %q", got)
	}
	bad := []TapdCLIRequest{
		{Command: "exec", Address: "tapbc1invalid"},
		{Command: TapdCLIUniverseSync, UniverseHost: "example.com;reboot"},
		{Command: TapdCLIMint, Name: "asset", Supply: 1, Metadata: "not-json"},
		{Command: TapdCLIGetInfo, Name: "unexpected"},
	}
	for _, request := range bad {
		if _, err := TapdCLIArgs(request); err == nil {
			t.Fatalf("unsafe tapcli request accepted: %#v", request)
		}
	}
}
