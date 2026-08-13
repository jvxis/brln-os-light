package appmanifest

import (
	"errors"
	"fmt"
)

const (
	BarkWalletID              = "bark-wallet"
	BarkWalletProject         = "bark-wallet"
	BarkWalletComposeFile     = "docker-compose.yaml"
	BarkWalletCaddyfile       = "Caddyfile"
	BarkWalletTLSDir          = "tls"
	BarkWalletPrimaryService  = "proxy"
	BarkWalletStopTimeout     = 60
	BarkWalletPort            = 4004
	BarkWalletAPIInternalPort = 4001
	BarkWalletDaemonPort      = 4000

	BarkWalletWebUID    = 101
	BarkWalletWebGID    = 101
	BarkWalletAPIUID    = 65530
	BarkWalletAPIGID    = 65530
	BarkWalletDaemonUID = 65531
	BarkWalletDaemonGID = 65531
	BarkWalletProxyUID  = 65532
	BarkWalletProxyGID  = 65532

	BarkWalletWebDigest               = "7f2b4469330f287192c981c64557d9469534fb4c4919bc846b829f59e4267655"
	BarkWalletAPIDigest               = "bf23f4b89e2c759d0d498f2d2a949b5d0f7fee29d8c1cc2b01a2882315363ffc"
	BarkWalletDaemonDigest            = "93bf4f806fb66aef06db071f88c8dff13ab44d5cad21bd94a9ab927ded3dcafc"
	BarkWalletProxyDigest             = "4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d"
	BarkWalletDaemonAMD64BinarySHA256 = "41ca75ae2e474b3a3dbb33f51af95175926abb2286134fcb435fb47b995a1efd"
	BarkWalletDaemonARM64BinarySHA256 = "a078495b095aab9826ccebc921dfad3cc1ae822e1610b94894f9833569552482"
	BarkWalletDaemonVersionOutput     = "barkd 0.6.1 (8134aed9029e4bbd5dff922500affd8611edb511)"

	BarkWalletWebImage    = "secondark/bark-web:0.7.2@sha256:" + BarkWalletWebDigest
	BarkWalletAPIImage    = "secondark/bark-web-api:0.7.2@sha256:" + BarkWalletAPIDigest
	BarkWalletDaemonImage = "secondark/bark:0.6.1@sha256:" + BarkWalletDaemonDigest
	BarkWalletProxyImage  = "caddy:2.10.2-alpine@sha256:" + BarkWalletProxyDigest

	BarkWalletImageWeb    AppImageVariant = "web"
	BarkWalletImageAPI    AppImageVariant = "api"
	BarkWalletImageDaemon AppImageVariant = "daemon"
	BarkWalletImageProxy  AppImageVariant = "proxy"
)

type BarkWalletComposePaths struct {
	WalletDir         string
	AdminPasswordPath string
	SessionSecretPath string
	CaddyfilePath     string
	TLSCertificate    string
	TLSPrivateKey     string
}

func BarkWalletImageForVariant(variant AppImageVariant) (string, error) {
	switch variant {
	case BarkWalletImageWeb:
		return BarkWalletWebImage, nil
	case BarkWalletImageAPI:
		return BarkWalletAPIImage, nil
	case BarkWalletImageDaemon:
		return BarkWalletDaemonImage, nil
	case BarkWalletImageProxy:
		return BarkWalletProxyImage, nil
	default:
		return "", errors.New("Bark Wallet image variant is not allowed")
	}
}

func BarkWalletImageVariants() []AppImageVariant {
	return []AppImageVariant{BarkWalletImageWeb, BarkWalletImageAPI, BarkWalletImageDaemon, BarkWalletImageProxy}
}

func BarkWalletImages() []string {
	return []string{BarkWalletWebImage, BarkWalletAPIImage, BarkWalletDaemonImage, BarkWalletProxyImage}
}

func BarkWalletCaddyConfig() string {
	return fmt.Sprintf(`{
	admin off
	auto_https off
}

https://:%d {
	tls /etc/caddy/tls/server.crt /etc/caddy/tls/server.key

	@direct_mnemonic path /api/barkd/api/v1/wallet/mnemonic
	handle @direct_mnemonic {
		respond 404
	}

	handle /api/* {
		reverse_proxy api:%d
	}

	handle_path /barkd-ws/* {
		reverse_proxy barkd:%d
	}

	handle {
		reverse_proxy web:8080
	}
}
`, BarkWalletPort, BarkWalletAPIInternalPort, BarkWalletDaemonPort)
}

func BarkWalletCompose(paths BarkWalletComposePaths) (string, error) {
	if paths.WalletDir == "" || paths.AdminPasswordPath == "" || paths.SessionSecretPath == "" ||
		paths.CaddyfilePath == "" || paths.TLSCertificate == "" || paths.TLSPrivateKey == "" {
		return "", errors.New("Bark Wallet compose path is invalid")
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
      - /var/cache/nginx:rw,noexec,nosuid,nodev,size=16m,uid=%d,gid=%d,mode=0700
      - /var/run:rw,noexec,nosuid,nodev,size=4m,uid=%d,gid=%d,mode=0700
    depends_on:
      - api
      - barkd

  api:
    image: %s
    restart: unless-stopped
    stop_grace_period: %ds
    init: true
    user: "%d:%d"
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    environment:
      PORT: "%d"
      WALLET_DIR: /wallet-data/.bark
      WALLET_DATA_PATH: %s/.bark/
      BARKD_URL: http://barkd:%d
      ARK_SERVER: https://ark.second.tech
      CHAIN_SOURCE: https://mempool.second.tech/api
      BARK_NETWORK: mainnet
      UI_AUTH: "true"
      UI_PASSWORD_FILE: /run/lightningos-auth/ui_password
      UI_SESSION_SECRET_FILE: /run/lightningos-auth/ui_session_secret
    tmpfs:
      - /tmp:rw,noexec,nosuid,nodev,size=16m,mode=1777
    volumes:
      - %s:/wallet-data:ro
      - %s:/run/lightningos-auth/ui_password:ro
      - %s:/run/lightningos-auth/ui_session_secret:ro
    depends_on:
      - barkd

  barkd:
    image: %s
    restart: unless-stopped
    stop_grace_period: %ds
    init: true
    user: "%d:%d"
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    entrypoint:
      - /usr/local/bin/barkd
    command:
      - --port
      - "%d"
      - --host
      - 0.0.0.0
      - --datadir
      - /data/.bark
      - --expose-mnemonic
    tmpfs:
      - /tmp:rw,noexec,nosuid,nodev,size=16m,mode=1777
    volumes:
      - %s:/data:rw

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
    tmpfs:
      - /tmp:rw,noexec,nosuid,nodev,size=16m,mode=1777
      - /run/lightningos-bin:rw,exec,nosuid,nodev,size=64m,uid=%d,gid=%d,mode=0700
      - /data:rw,noexec,nosuid,nodev,size=16m,uid=%d,gid=%d,mode=0700
      - /config:rw,noexec,nosuid,nodev,size=16m,uid=%d,gid=%d,mode=0700
    volumes:
      - %s:/etc/caddy/Caddyfile:ro
      - %s:/etc/caddy/tls/server.crt:ro
      - %s:/etc/caddy/tls/server.key:ro
    depends_on:
      - web
      - api
      - barkd

networks:
  default:
    name: bark-wallet_default
`, BarkWalletWebImage, BarkWalletStopTimeout, BarkWalletWebUID, BarkWalletWebGID,
		BarkWalletWebUID, BarkWalletWebGID, BarkWalletWebUID, BarkWalletWebGID,
		BarkWalletAPIImage, BarkWalletStopTimeout, BarkWalletAPIUID, BarkWalletAPIGID,
		BarkWalletAPIInternalPort, paths.WalletDir, BarkWalletDaemonPort, paths.WalletDir,
		paths.AdminPasswordPath, paths.SessionSecretPath, BarkWalletDaemonImage,
		BarkWalletStopTimeout, BarkWalletDaemonUID, BarkWalletDaemonGID, BarkWalletDaemonPort,
		paths.WalletDir, BarkWalletProxyImage, BarkWalletStopTimeout, BarkWalletProxyUID,
		BarkWalletProxyGID, BarkWalletPort, BarkWalletPort, BarkWalletProxyUID,
		BarkWalletProxyGID, BarkWalletProxyUID, BarkWalletProxyGID, BarkWalletProxyUID,
		BarkWalletProxyGID, paths.CaddyfilePath, paths.TLSCertificate, paths.TLSPrivateKey), nil
}
