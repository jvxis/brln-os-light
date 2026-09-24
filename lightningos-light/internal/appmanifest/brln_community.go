package appmanifest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// BR⚡LN Community is the members chat of the BR⚡LN Club (test build). The node
// runs a local copy of the chat and the member's Nostr remote signer (NIP-46).
// The key never leaves the signer data directory; the chat relay is operated by
// the club and is reached over an outbound WebSocket only.
//
// The node also runs its own NIP-46 pairing relay next to the signer, so the chat
// and the signer on the same node do not need the club's server to talk to each
// other. The club's pairing relay stays in every pairing link for devices that
// cannot reach the node.
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
	BRLNCommunityPairingPort     = 3335
	BRLNCommunityPairingPath     = "/pairing"

	BRLNCommunityWebUID    = 101
	BRLNCommunityWebGID    = 101
	BRLNCommunitySignerUID = 65529
	BRLNCommunitySignerGID = 65529
	BRLNCommunityProxyUID  = 65532
	BRLNCommunityProxyGID  = 65532
	// The pairing relay runs from the signer image but never as the signer: it holds
	// nothing and mounts nothing, and a separate identity keeps it that way.
	BRLNCommunityPairingUID = 65528
	BRLNCommunityPairingGID = 65528

	BRLNCommunityRelease             = "0.1.33"
	BRLNCommunityWebDigest           = "e6529601623c9d0baee44e318567a90f17225ad2de115e3aa759835de6392f8a"
	BRLNCommunitySignerDigest        = "b10855fac711cd6217398c6ffa85ec3dddcaa8ce7a8c50a1ba4552733af0ae4c"
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

		# The node's own pairing relay. It only carries NIP-46 messages, which are
		# end-to-end encrypted between device and signer, so it sits beside the chat
		# rather than behind the LightningOS reauthentication.
		@pairing path %s %s/
		handle @pairing {
			rewrite * /
			reverse_proxy pairing:%d
		}

		handle {
			reverse_proxy web:%d
		}
	}
}
`, BRLNCommunityPort, BRLNCommunitySignerPort, BRLNCommunitySignerPort,
		BRLNCommunityPairingPath, BRLNCommunityPairingPath, BRLNCommunityPairingPort,
		BRLNCommunityWebInternalPort)
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
      LOCAL_RELAY: ws://pairing:%d
      LOCAL_RELAY_PATH: %s
      KEY_PASSWORD_FILE: /run/lightningos-auth/key_password
      ACCESS_PASSWORD_FILE: /run/lightningos-auth/access_password
    volumes:
      - %s:/data:rw
      - %s:/run/lightningos-auth:ro
    depends_on:
      - pairing

  pairing:
    image: %s
    restart: unless-stopped
    stop_grace_period: %ds
    user: "%d:%d"
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    entrypoint:
      - /brln-signer-relay
    environment:
      PORT: "%d"

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
      - pairing

networks:
  default:
    name: brln-community_default
`, BRLNCommunityWebImage, BRLNCommunityStopTimeout, BRLNCommunityWebUID, BRLNCommunityWebGID,
		BRLNCommunitySignerImage, BRLNCommunityStopTimeout, BRLNCommunitySignerUID, BRLNCommunitySignerGID,
		BRLNCommunitySignerPort, BRLNCommunitySignerRelay,
		BRLNCommunityPairingPort, BRLNCommunityPairingPath,
		paths.SignerDir, paths.AuthDir,
		BRLNCommunitySignerImage, BRLNCommunityStopTimeout, BRLNCommunityPairingUID, BRLNCommunityPairingGID,
		BRLNCommunityPairingPort,
		BRLNCommunityProxyImage, BRLNCommunityStopTimeout, BRLNCommunityProxyUID, BRLNCommunityProxyGID,
		brlnCommunityProxyConfigHash(),
		BRLNCommunityPort, BRLNCommunityPort,
		BRLNCommunityProxyUID, BRLNCommunityProxyGID, BRLNCommunityProxyUID, BRLNCommunityProxyGID,
		BRLNCommunityProxyUID, BRLNCommunityProxyGID,
		paths.CaddyfilePath, paths.TLSCertificate, paths.TLSPrivateKey, paths.ManagerCACertificate), nil
}
