package appmanifest

import (
	"strings"
	"testing"
)

func testBRLNCommunityComposePaths() BRLNCommunityComposePaths {
	return BRLNCommunityComposePaths{
		SignerDir:            "/var/lib/lightningos/apps-data/brln-community/signer",
		AuthDir:              "/var/lib/lightningos/apps-data/brln-community/auth",
		CaddyfilePath:        "/var/lib/lightningos-privileged/apps/brln-community/Caddyfile",
		TLSCertificate:       "/var/lib/lightningos-privileged/apps/brln-community/tls/server.crt",
		TLSPrivateKey:        "/var/lib/lightningos-privileged/apps/brln-community/tls/server.key",
		ManagerCACertificate: "/etc/lightningos/tls/local-ca.crt",
	}
}

func TestBRLNCommunityCatalogUsesImmutableReleaseImages(t *testing.T) {
	images := BRLNCommunityImages()
	if len(images) != 3 {
		t.Fatalf("images=%#v", images)
	}
	for _, image := range images {
		if !strings.Contains(image, "@sha256:") || strings.Contains(image, ":latest") || strings.Contains(image, "@sha256:0000") {
			t.Fatalf("mutable BR⚡LN Community image: %q", image)
		}
	}
	if !strings.HasPrefix(BRLNCommunityWebImage, "ghcr.io/jvxis/brln-community-web:"+BRLNCommunityRelease+"@") ||
		!strings.HasPrefix(BRLNCommunitySignerImage, "ghcr.io/jvxis/brln-signer:"+BRLNCommunityRelease+"@") ||
		BRLNCommunitySignerVersionOutput != "brln-signer "+BRLNCommunityRelease {
		t.Fatalf("unexpected BR⚡LN Community release set: %#v", images)
	}
	for _, variant := range BRLNCommunityImageVariants() {
		if _, err := BRLNCommunityImageForVariant(variant); err != nil {
			t.Fatalf("variant %q rejected: %v", variant, err)
		}
	}
	if _, err := BRLNCommunityImageForVariant("signer;reboot"); err == nil {
		t.Fatal("untrusted BR⚡LN Community image variant accepted")
	}
}

func TestBRLNCommunityComposeIsClosedAndLeastPrivilege(t *testing.T) {
	compose, err := BRLNCommunityCompose(testBRLNCommunityComposePaths())
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		BRLNCommunityWebImage, BRLNCommunitySignerImage, BRLNCommunityProxyImage,
		"read_only: true", "cap_drop:", "no-new-privileges:true",
		`user: "101:101"`, `user: "65529:65529"`, `user: "65532:65532"`,
		"RELAYS: wss://signer.br-ln.com",
		"/var/lib/lightningos/apps-data/brln-community/signer:/data:rw",
		"/var/lib/lightningos/apps-data/brln-community/auth:/run/lightningos-auth:ro",
		"host.docker.internal:host-gateway", "/etc/caddy/manager-ca.crt:ro",
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("compose is missing %q", required)
		}
	}
	for _, forbidden := range []string{"privileged: true", "/var/run/docker.sock", `user: "0:0"`, ":latest", "network_mode", ".lnd", "macaroon", "bitcoin"} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("compose contains forbidden capability %q", forbidden)
		}
	}
	if strings.Count(compose, `"4448:4448"`) != 1 || strings.Count(compose, "ports:") != 1 {
		t.Fatalf("unexpected published ports:\n%s", compose)
	}
	if strings.Count(compose, "apps-data/brln-community/signer") != 1 {
		t.Fatal("only the signer may mount the member key directory")
	}
}

func TestBRLNCommunitySignerIdentityIsSeparate(t *testing.T) {
	for _, other := range []int{BRLNCommunityWebUID, BRLNCommunityProxyUID, BarkWalletAPIUID, BarkWalletDaemonUID, BarkWalletProxyUID, LNbitsContainerUID, PublicPoolContainerUID} {
		if other == BRLNCommunitySignerUID {
			t.Fatalf("signer UID %d is shared with another container", BRLNCommunitySignerUID)
		}
	}
}

func TestBRLNCommunityProxyProtectsSignerChanges(t *testing.T) {
	config := BRLNCommunityCaddyConfig()
	for _, required := range []string{
		"https://:4448", "tls /etc/caddy/tls/server.crt /etc/caddy/tls/server.key",
		"@signer_sensitive path /signer/api/key /signer/api/backup /signer/api/pairings /signer/api/nostrconnect",
		"forward_auth https://host.docker.internal:8443",
		"uri /api/apps/brln-community/signer-authorization",
		"header_up -Authorization",
		"tls_trust_pool file /etc/caddy/manager-ca.crt", "tls_server_name localhost",
		"handle_path /signer/*", "reverse_proxy signer:8081", "reverse_proxy web:8080",
		"protocols h1 h2",
	} {
		if !strings.Contains(config, required) {
			t.Fatalf("proxy config is missing %q", required)
		}
	}
	if strings.Index(config, "@signer_sensitive") > strings.Index(config, "handle_path /signer/*") {
		t.Fatal("protected signer routes must be matched before the open signer routes")
	}
	if strings.Contains(config, "tls_insecure_skip_verify") {
		t.Fatal("proxy disables manager TLS verification")
	}
}

func TestBRLNCommunityComposeFollowsTheProxyConfiguration(t *testing.T) {
	compose, err := BRLNCommunityCompose(testBRLNCommunityComposePaths())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compose, "CONFIG_HASH: "+brlnCommunityProxyConfigHash()) {
		t.Fatal("the proxy service does not pin the configuration it serves")
	}
	if len(brlnCommunityProxyConfigHash()) != 16 {
		t.Fatalf("unexpected hash: %q", brlnCommunityProxyConfigHash())
	}
}

func TestBRLNCommunityComposeRejectsMissingBrokerPath(t *testing.T) {
	paths := testBRLNCommunityComposePaths()
	paths.ManagerCACertificate = ""
	if _, err := BRLNCommunityCompose(paths); err == nil {
		t.Fatal("missing broker-owned path accepted")
	}
}
