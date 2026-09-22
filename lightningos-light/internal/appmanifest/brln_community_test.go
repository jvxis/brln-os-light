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
		"LOCAL_RELAY: ws://pairing:3335", "LOCAL_RELAY_PATH: /pairing",
		"- /brln-signer-relay", `user: "65528:65528"`, `PORT: "3335"`,
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

	// The pairing relay holds nothing and reaches nothing but its own port: no
	// volume, no published port, no host access. It runs from the signer image,
	// so this is what keeps it from ever becoming a second way into the key.
	pairing := composeService(t, compose, "pairing")
	if !strings.Contains(pairing, BRLNCommunitySignerImage) || !strings.Contains(pairing, "- /brln-signer-relay") {
		t.Fatalf("pairing relay does not run the relay from the pinned signer image:\n%s", pairing)
	}
	for _, forbidden := range []string{"volumes:", "ports:", "extra_hosts:", "apps-data", "lightningos-auth", `user: "65529:65529"`} {
		if strings.Contains(pairing, forbidden) {
			t.Fatalf("pairing relay has %q:\n%s", forbidden, pairing)
		}
	}
}

// composeService returns one service block of a generated compose file.
func composeService(t *testing.T, compose, name string) string {
	t.Helper()
	lines := strings.Split(compose, "\n")
	for i, line := range lines {
		if line != "  "+name+":" {
			continue
		}
		block := []string{line}
		for _, next := range lines[i+1:] {
			if next != "" && !strings.HasPrefix(next, "    ") {
				break
			}
			block = append(block, next)
		}
		return strings.Join(block, "\n")
	}
	t.Fatalf("compose has no %q service", name)
	return ""
}

func TestBRLNCommunitySignerIdentityIsSeparate(t *testing.T) {
	for _, other := range []int{BRLNCommunityWebUID, BRLNCommunityProxyUID, BRLNCommunityPairingUID, BarkWalletAPIUID, BarkWalletDaemonUID, BarkWalletProxyUID, LNbitsContainerUID, PublicPoolContainerUID} {
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
		"@pairing path /pairing /pairing/", "reverse_proxy pairing:3335",
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

	// The pairing relay must answer before the chat's catch-all, and never behind
	// the reauthentication that guards key changes: a device asking for a
	// signature is not changing the key, and the manager would refuse it.
	pairing := strings.Index(config, "handle @pairing")
	if pairing < 0 || pairing > strings.Index(config, "reverse_proxy web:") {
		t.Fatal("the pairing route must come before the chat's catch-all")
	}
	block := config[pairing:]
	block = block[:strings.Index(block, "}")]
	if strings.Contains(block, "forward_auth") {
		t.Fatal("the pairing relay is behind the LightningOS reauthentication")
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
