package appmanifest

import (
	"gopkg.in/yaml.v3"
	"net"
	"strings"
	"testing"
)

func testHardwalletPaths() HardwalletPaths {
	return HardwalletPaths{Root: "/snapshot/hw", Compose: "/snapshot/hw/docker-compose.yaml", Caddy: "/snapshot/hw/Caddyfile", Token: "/snapshot/hw/proxy_token", Settings: "/snapshot/hw/settings.json", Certificate: "/snapshot/hw/server.crt", PrivateKey: "/snapshot/hw/server.key", ManagerCA: "/etc/lightningos/tls/local-ca.crt"}
}
func TestHardwalletTransportBoundary(t *testing.T) {
	for _, source := range []string{"local", "club"} {
		raw, err := HardwalletCompose(testHardwalletPaths(), HardwalletSettings{Source: source, BindIP: "100.100.1.1"})
		if err != nil {
			t.Fatal(err)
		}
		var doc catalogCompose
		if err = yaml.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatal(err)
		}
		if len(doc.Services) != 2 {
			t.Fatal("unexpected service count")
		}
		for _, forbidden := range []string{"docker.sock", "/dev/", "macaroon", "lnd/data", "privileged: true", "network_mode: host", "0.0.0.0:"} {
			if strings.Contains(raw, forbidden) {
				t.Fatal(forbidden)
			}
		}
		if strings.Contains(raw, "electrs_default") != (source == "local") {
			t.Fatal("incorrect Electrs network dependency")
		}
		if !strings.Contains(raw, "100.100.1.1:4449:4449") {
			t.Fatal("must bind tailnet only")
		}
		if source == "club" {
			host, _, err := net.SplitHostPort(HardwalletClubAddress)
			if err != nil || net.ParseIP(host) != nil {
				t.Fatal("club access must use its public DNS name, not a personal tailnet IP")
			}
			if !strings.Contains(raw, `BRLN_ELECTRUM_TRANSPORT: "tcp"`) || strings.Contains(raw, `BRLN_ELECTRUM_TRANSPORT: "tailscale"`) {
				t.Fatal("club Electrum must not require tailnet membership")
			}
		}
	}
	for _, ip := range []string{"127.0.0.1", "0.0.0.0", "192.168.1.2", "8.8.8.8", "100.128.0.1"} {
		if ValidateHardwalletSettings(HardwalletSettings{Source: "local", BindIP: ip}) == nil {
			t.Fatal(ip)
		}
	}
}
func TestHardwalletProxyProtectsAllRoutes(t *testing.T) {
	raw, err := HardwalletCaddy(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"forward_auth https://host.docker.internal:8443", "/api/apps/brln-hardwallet/authorization", "header_up -Cookie", "header_up X-BRLN-Proxy-Token", "tls_trust_pool file"} {
		if !strings.Contains(raw, s) {
			t.Fatal(s)
		}
	}
	if strings.Contains(raw, "tls_insecure_skip_verify") || strings.Contains(raw, "handle_path") {
		t.Fatal("unsafe proxy bypass")
	}
	if _, err = HardwalletCaddy("untrusted\nroute"); err == nil {
		t.Fatal("accepted config injection")
	}
}
