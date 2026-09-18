package appmanifest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// BR⚡LN Community is the members chat of the BR⚡LN Club (test build). The node
// runs a local copy of the chat and the member's Nostr remote signer (NIP-46).
// The key never leaves the signer data directory; the relays are operated by
// the club and are reached over outbound WebSockets only.
const (
	BRLNCommunityID              = "brln-community"
	BRLNCommunityProject         = "brln-community"
	BRLNCommunityComposeFile     = "docker-compose.yaml"
	BRLNCommunityCaddyfile       = "Caddyfile"
	BRLNCommunityTLSDir          = "tls"
	BRLNCommunityPrimaryService  = "proxy"
	BRLNCommunityStopTimeout     = 30
	BRLNCommunityPort            = 4448
	BRLNCommunityWebInternalPort = 8080
	BRLNCommunitySignerPort      = 8081
	BRLNCommunitySignerRelay     = "wss://signer.br-ln.com"

	BRLNCommunityWebUID    = 101
	BRLNCommunityWebGID    = 101
	BRLNCommunitySignerUID = 65529
	BRLNCommunitySignerGID = 65529
	BRLNCommunityProxyUID  = 65532
	BRLNCommunityProxyGID  = 65532

	BRLNCommunityRelease             = "0.1.4"
	BRLNCommunityWebDigest           = "3aec012bfdb29f3e4cf6f7626b25333a8505844c56decb6d522ffbc211cd68d6"
	BRLNCommunitySignerDigest        = "c1c5b282cd0fa5ea5720f7c021a5fa32da6c7a00b1e45552193d207c89a3b2bc"
	BRLNCommunitySignerVersionOutput = "brln-signer " + BRLNCommunityRelease

	BRLNCommunityWebImage    = "ghcr.io/jvxis/brln-community-web:" + BRLNCommunityRelease + "@sha256:" + BRLNCommunityWebDigest
	BRLNCommunitySignerImage = "ghcr.io/jvxis/brln-signer:" + BRLNCommunityRelease + "@sha256:" + BRLNCommunitySignerDigest
	// The proxy is the same official Caddy release Bark Wallet pins.
	BRLNCommunityProxyImage = BarkWalletProxyImage

	BRLNCommunityImageWeb    AppImageVariant = "web"
	BRLNCommunityImageSigner AppImageVariant = "signer"
	BRLNCommunityImageProxy  AppImageVariant = "proxy"
)

type BRLNCommunityComposePaths struct {
	SignerDir            string
	AuthDir              string
	CaddyfilePath        string
	TLSCertificate       string
	TLSPrivateKey        string
	ManagerCACertificate string
}

func BRLNCommunityImageForVariant(variant AppImageVariant) (string, error) {
	switch variant {
	case BRLNCommunityImageWeb:
		return BRLNCommunityWebImage, nil
	case BRLNCommunityImageSigner:
		return BRLNCommunitySignerImage, nil
	case BRLNCommunityImageProxy:
		return BRLNCommunityProxyImage, nil
	default:
		return "", errors.New("BR⚡LN Community image variant is not allowed")
	}
}

func BRLNCommunityImageVariants() []AppImageVariant {
	return []AppImageVariant{BRLNCommunityImageWeb, BRLNCommunityImageSigner, BRLNCommunityImageProxy}
}

func BRLNCommunityImages() []string {
	return []string{BRLNCommunityWebImage, BRLNCommunitySignerImage, BRLNCommunityProxyImage}
}

// BRLNCommunityCaddyConfig serves the chat at / and the signer at /signer/.
// Signer calls that set up or export the key, or pair a new device, also need a
// recent LightningOS reauthentication through forward_auth.
func BRLNCommunityCaddyConfig() string {
	return fmt.Sprintf(`{
	admin off
	auto_https off

	# HTTP/3 brings nothing on a LAN and breaks the browser exception for the
	# app's self-signed certificate: the page loads, then QUIC requests fail.
	servers {
		protocols h1 h2
	}
}

https://:%d {
	tls /etc/caddy/tls/server.crt /etc/caddy/tls/server.key

	route {
		redir /signer /signer/ 308

		@signer_sensitive path /signer/api/key /signer/api/backup /signer/api/pairings /signer/api/nostrconnect
		handle @signer_sensitive {
			forward_auth https://host.docker.internal:8443 {
				uri /api/apps/brln-community/signer-authorization
				header_up -Authorization
				transport http {
					tls_trust_pool file /etc/caddy/manager-ca.crt
					tls_server_name localhost
				}
			}
			uri strip_prefix /signer
			reverse_proxy signer:%d
		}

		handle_path /signer/* {
			reverse_proxy signer:%d
		}

		handle {
			reverse_proxy web:%d
		}
	}
}
`, BRLNCommunityPort, BRLNCommunitySignerPort, BRLNCommunitySignerPort, BRLNCommunityWebInternalPort)
}

// brlnCommunityProxyConfigHash pins the proxy service to its configuration, so a
// change in the Caddyfile recreates the container. Compose only compares service
// definitions, never the contents of a bind-mounted file.
func brlnCommunityProxyConfigHash() string {
	sum := sha256.Sum256([]byte(BRLNCommunityCaddyConfig()))
	return hex.EncodeToString(sum[:8])
}

func BRLNCommunityCompose(paths BRLNCommunityComposePaths) (string, error) {
	if paths.SignerDir == "" || paths.AuthDir == "" || paths.CaddyfilePath == "" || paths.TLSCertificate == "" ||
		paths.TLSPrivateKey == "" || paths.ManagerCACertificate == "" {
		return "", errors.New("BR⚡LN Community compose path is invalid")
	}
	return fmt.Sprintf(`services:
  web:
    image: %s
    restart: unless-stopped
    stop_grace_period: %ds
    user: "%d:%d"
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    tmpfs:
      - /tmp:rw,noexec,nosuid,nodev,size=16m,mode=1777

  signer:
    image: %s
    restart: unless-stopped
    stop_grace_period: %ds
    user: "%d:%d"
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    environment:
      DATA_DIR: /data
      LISTEN: ":%d"
      RELAYS: %s
      KEY_PASSWORD_FILE: /run/lightningos-auth/key_password
      ACCESS_PASSWORD_FILE: /run/lightningos-auth/access_password
    volumes:
      - %s:/data:rw
      - %s:/run/lightningos-auth:ro

  proxy:
    image: %s
    restart: unless-stopped
    stop_grace_period: %ds
    user: "%d:%d"
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    environment:
      CONFIG_HASH: %s
    entrypoint:
      - /bin/sh
      - -c
    command:
      - |
        set -eu
        cp /usr/bin/caddy /run/lightningos-bin/caddy
        chmod 0500 /run/lightningos-bin/caddy
        exec /run/lightningos-bin/caddy run --config /etc/caddy/Caddyfile
    ports:
      - "%d:%d"
    extra_hosts:
      - "host.docker.internal:host-gateway"
    tmpfs:
      - /tmp:rw,noexec,nosuid,nodev,size=16m,mode=1777
      - /run/lightningos-bin:rw,exec,nosuid,nodev,size=64m,uid=%d,gid=%d,mode=0700
      - /data:rw,noexec,nosuid,nodev,size=16m,uid=%d,gid=%d,mode=0700
      - /config:rw,noexec,nosuid,nodev,size=16m,uid=%d,gid=%d,mode=0700
    volumes:
      - %s:/etc/caddy/Caddyfile:ro
      - %s:/etc/caddy/tls/server.crt:ro
      - %s:/etc/caddy/tls/server.key:ro
      - %s:/etc/caddy/manager-ca.crt:ro
    depends_on:
      - web
      - signer

networks:
  default:
    name: brln-community_default
`, BRLNCommunityWebImage, BRLNCommunityStopTimeout, BRLNCommunityWebUID, BRLNCommunityWebGID,
		BRLNCommunitySignerImage, BRLNCommunityStopTimeout, BRLNCommunitySignerUID, BRLNCommunitySignerGID,
		BRLNCommunitySignerPort, BRLNCommunitySignerRelay, paths.SignerDir, paths.AuthDir,
		BRLNCommunityProxyImage, BRLNCommunityStopTimeout, BRLNCommunityProxyUID, BRLNCommunityProxyGID,
		brlnCommunityProxyConfigHash(),
		BRLNCommunityPort, BRLNCommunityPort,
		BRLNCommunityProxyUID, BRLNCommunityProxyGID, BRLNCommunityProxyUID, BRLNCommunityProxyGID,
		BRLNCommunityProxyUID, BRLNCommunityProxyGID,
		paths.CaddyfilePath, paths.TLSCertificate, paths.TLSPrivateKey, paths.ManagerCACertificate), nil
}
