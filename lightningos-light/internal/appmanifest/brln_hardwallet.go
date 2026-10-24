package appmanifest

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

const (
	HardwalletID       = "brln-hardwallet"
	HardwalletPort     = 4449
	HardwalletUID      = 65527
	HardwalletProxyGID = 65532
	// Filled only from a successful published multi-architecture build; no tag fallback.
	HardwalletImage         = "ghcr.io/jvxis/brln-hardwallet:sha-595a505f764c4361e3f1165baf634edd188193d4@sha256:8617dbab651db7abed94c7a3936c24295d9abe8d27ebf633e0da9299dc887ad2"
	HardwalletProxyImage    = BarkWalletProxyImage
	HardwalletVersionOutput = "brln-hardwallet 0.1.0-dev"
	HardwalletClubAddress   = "electrum.br-ln.com:50001"
)

type HardwalletPaths struct{ Root, Compose, Caddy, Token, Settings, Certificate, PrivateKey, ManagerCA string }
type HardwalletSettings struct {
	Source string `json:"source"`
	// Read the original single-address pilot during migration; new snapshots use BindIPs.
	BindIP  string   `json:"bind_ip,omitempty"`
	BindIPs []string `json:"bind_ips,omitempty"`
}

func HardwalletBindIPs(s HardwalletSettings) []string {
	if s.BindIP != "" {
		return []string{s.BindIP}
	}
	return s.BindIPs
}

func HardwalletPrivateIPv4(address string) bool {
	ip := net.ParseIP(address)
	_, tailnet, _ := net.ParseCIDR("100.64.0.0/10")
	return ip != nil && ip.To4() != nil && address == ip.String() && (ip.IsPrivate() || tailnet.Contains(ip))
}

func ValidateHardwalletSettings(s HardwalletSettings) error {
	if s.Source != "local" && s.Source != "club" {
		return errors.New("unknown Electrum source")
	}
	addresses := HardwalletBindIPs(s)
	if len(addresses) == 0 || len(addresses) > 32 || (s.BindIP != "" && len(s.BindIPs) != 0) {
		return errors.New("private IPv4 bind addresses are required")
	}
	seen := map[string]bool{}
	for _, address := range addresses {
		if !HardwalletPrivateIPv4(address) || seen[address] {
			return errors.New("invalid or duplicate private IPv4 bind address")
		}
		seen[address] = true
	}
	if s.Source == "club" && HardwalletClubAddress == "" {
		return errors.New("club Electrum transport has not been verified")
	}
	return nil
}
func HardwalletImageForVariant(v AppImageVariant) (string, error) {
	switch v {
	case "app":
		if HardwalletImage != "" {
			return HardwalletImage, nil
		}
	case "proxy":
		return HardwalletProxyImage, nil
	}
	return "", errors.New("verified Hardwallet image unavailable")
}
func HardwalletCaddy(token string) (string, error) {
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(token) {
		return "", errors.New("invalid internal proxy token")
	}
	return fmt.Sprintf(`{
 admin off
 auto_https off
 servers {
  protocols h1 h2
 }
}
https://:4449 {
 tls /etc/caddy/server.crt /etc/caddy/server.key
 route {
  forward_auth https://host.docker.internal:8443 {
   uri /api/apps/brln-hardwallet/authorization
   header_up -Authorization
   transport http {
    tls_trust_pool file /etc/caddy/manager-ca.crt
    tls_server_name localhost
   }
  }
  reverse_proxy app:8080 {
   header_up X-BRLN-Proxy-Token %s
   header_up -Cookie
   header_up -Authorization
  }
 }
}
`, token), nil
}
func HardwalletCompose(p HardwalletPaths, s HardwalletSettings) (string, error) {
	if err := ValidateHardwalletSettings(s); err != nil {
		return "", err
	}
	if HardwalletImage == "" {
		return "", errors.New("verified Hardwallet image unavailable")
	}
	for _, path := range []string{p.Root, p.Compose, p.Caddy, p.Token, p.Settings, p.Certificate, p.PrivateKey, p.ManagerCA} {
		if path == "" {
			return "", errors.New("missing Hardwallet path")
		}
	}
	address, transport, network, networks := "electrs:50001", "local", "      - electrum\n", "  electrum:\n    external: true\n    name: electrs_default\n"
	if s.Source == "club" {
		address = HardwalletClubAddress
		transport = "tcp"
		network = ""
		networks = ""
	}
	ports := ""
	for _, address := range HardwalletBindIPs(s) {
		ports += fmt.Sprintf("      - \"%s:4449:4449\"\n", address)
	}
	return fmt.Sprintf(`services:
  app:
    image: %s
    restart: unless-stopped
    user: "65527:65527"
    read_only: true
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
    pids_limit: 64
    mem_limit: 128m
    environment:
      BRLN_AUTH_TOKEN_FILE: /run/auth/proxy_token
      BRLN_ELECTRUM_ADDR: %q
      BRLN_ELECTRUM_TRANSPORT: %q
      BRLN_ELECTRUM_SOURCE: %q
    volumes:
      - %s:/run/auth/proxy_token:ro
    networks:
      - default
%s
  proxy:
    image: %s
    restart: unless-stopped
    user: "65532:65532"
    read_only: true
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
    pids_limit: 64
    mem_limit: 128m
    entrypoint: [/bin/sh, -c]
    command:
      - |
        set -eu
        cp /usr/bin/caddy /run/lightningos-bin/caddy
        chmod 0500 /run/lightningos-bin/caddy
        exec /run/lightningos-bin/caddy run --config /etc/caddy/Caddyfile
    ports:
%s
    extra_hosts: ["host.docker.internal:host-gateway"]
    tmpfs:
      - /tmp:rw,noexec,nosuid,nodev,size=16m,mode=1777
      - /run/lightningos-bin:rw,exec,nosuid,nodev,size=64m,uid=65532,gid=65532,mode=0700
      - /data:rw,noexec,nosuid,nodev,size=16m,uid=65532,gid=65532,mode=0700
      - /config:rw,noexec,nosuid,nodev,size=16m,uid=65532,gid=65532,mode=0700
    volumes:
      - %s:/etc/caddy/Caddyfile:ro
      - %s:/etc/caddy/server.crt:ro
      - %s:/etc/caddy/server.key:ro
      - %s:/etc/caddy/manager-ca.crt:ro
    depends_on: [app]
networks:
  default:
    name: brln-hardwallet_default
%s`, HardwalletImage, address, transport, s.Source, p.Token, network, HardwalletProxyImage, strings.TrimSuffix(ports, "\n"), p.Caddy, p.Certificate, p.PrivateKey, p.ManagerCA, networks), nil
}
