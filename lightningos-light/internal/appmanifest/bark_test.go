package appmanifest

import (
	"strings"
	"testing"
)

func testBarkWalletComposePaths() BarkWalletComposePaths {
	return BarkWalletComposePaths{
		WalletDir:            "/var/lib/lightningos/apps-data/bark-wallet/wallet",
		AdminPasswordPath:    "/var/lib/lightningos/apps-data/bark-wallet/auth/ui_password",
		SessionSecretPath:    "/var/lib/lightningos/apps-data/bark-wallet/auth/ui_session_secret",
		CaddyfilePath:        "/var/lib/lightningos-privileged/apps/bark-wallet/Caddyfile",
		TLSCertificate:       "/var/lib/lightningos-privileged/apps/bark-wallet/tls/server.crt",
		TLSPrivateKey:        "/var/lib/lightningos-privileged/apps/bark-wallet/tls/server.key",
		ManagerCACertificate: "/etc/lightningos/tls/local-ca.crt",
	}
}

func TestBarkWalletCatalogUsesImmutableOfficialReleaseImages(t *testing.T) {
	images := BarkWalletImages()
	if len(images) != 4 {
		t.Fatalf("images=%#v", images)
	}
	for _, image := range images {
		if !strings.Contains(image, "@sha256:") || strings.Contains(image, ":latest") {
			t.Fatalf("mutable Bark Wallet image: %q", image)
		}
	}
	if !strings.Contains(BarkWalletWebImage, "secondark/bark-web:0.7.2@") ||
		!strings.Contains(BarkWalletAPIImage, "secondark/bark-web-api:0.7.2@") ||
		!strings.Contains(BarkWalletDaemonImage, "secondark/bark:0.6.1@") {
		t.Fatalf("unexpected Bark release set: %#v", images)
	}
	for _, variant := range BarkWalletImageVariants() {
		if _, err := BarkWalletImageForVariant(variant); err != nil {
			t.Fatalf("variant %q rejected: %v", variant, err)
		}
	}
	if _, err := BarkWalletImageForVariant("web;reboot"); err == nil {
		t.Fatal("untrusted Bark image variant accepted")
	}
}

func TestBarkWalletComposeIsClosedAndLeastPrivilege(t *testing.T) {
	compose, err := BarkWalletCompose(testBarkWalletComposePaths())
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		BarkWalletWebImage, BarkWalletAPIImage, BarkWalletDaemonImage, BarkWalletProxyImage,
		`UI_AUTH: "true"`, "--expose-mnemonic", "read_only: true", "cap_drop:",
		"no-new-privileges:true", `user: "65530:65530"`, `user: "65531:65531"`, `user: "65532:65532"`,
		"entrypoint:\n      - /usr/local/bin/barkd",
		"/run/lightningos-auth/ui_password:ro", "/run/lightningos-auth/ui_session_secret:ro",
		"host.docker.internal:host-gateway", "/etc/caddy/manager-ca.crt:ro",
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("compose is missing %q", required)
		}
	}
	if BarkWalletDaemonAMD64BinarySHA256 == "" || BarkWalletDaemonARM64BinarySHA256 == "" || BarkWalletDaemonVersionOutput == "" {
		t.Fatal("Bark daemon binary attestation is incomplete")
	}
	for _, forbidden := range []string{"privileged: true", "/var/run/docker.sock", `user: "0:0"`, ":latest"} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("compose contains forbidden capability %q", forbidden)
		}
	}
	if strings.Count(compose, `"4004:4004"`) != 1 {
		t.Fatalf("unexpected published ports:\n%s", compose)
	}
}

func TestBarkWalletProxyBlocksDirectMnemonicAndPreservesProtectedRoute(t *testing.T) {
	config := BarkWalletCaddyConfig()
	for _, required := range []string{
		"/api/barkd/api/v1/wallet/mnemonic", "respond 404", "handle /api/*",
		"/api/reveal-mnemonic", "forward_auth https://host.docker.internal:8443",
		"uri /api/apps/bark-wallet/reveal-authorization", "tls_trust_pool file /etc/caddy/manager-ca.crt",
		"tls_server_name localhost",
		"handle_path /barkd-ws/*", "tls /etc/caddy/tls/server.crt /etc/caddy/tls/server.key",
	} {
		if !strings.Contains(config, required) {
			t.Fatalf("proxy config is missing %q", required)
		}
	}
	if strings.Contains(config, "tls_insecure_skip_verify") {
		t.Fatal("proxy disables manager TLS verification")
	}
}

func TestBarkWalletComposeRejectsMissingBrokerPath(t *testing.T) {
	paths := testBarkWalletComposePaths()
	paths.ManagerCACertificate = ""
	if _, err := BarkWalletCompose(paths); err == nil {
		t.Fatal("missing broker-owned path accepted")
	}
}
