package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lightningos-light/internal/system"
)

const (
	barkWalletAppID = "bark-wallet"
	barkWalletPort  = 4004

	// These are the multi-architecture image digests shipped by the official
	// Bark Wallet 0.3.0 Umbrel package. Keep the web, API, and daemon versions in
	// lockstep and never replace the digests with :latest.
	barkWalletWebImage    = "secondark/bark-web:0.3.0@sha256:d00308ef6739243ea7d5187dffe250851857deb88aa93acc9527dd23d1288ef2"
	barkWalletAPIImage    = "secondark/bark-web-api:0.3.0@sha256:846086e4507ea1827de51aa91a8fd22ddbb93816aff6c182c704f22fd0af720a"
	barkWalletDaemonImage = "secondark/bark:0.3.0@sha256:4cfa73136f8098af164283aa2d5a79f1d46005ea7568fff8c6ae130abfd9c599"
	barkWalletProxyImage  = "caddy:2.8-alpine@sha256:af32e97399febea808609119bb21544d0265c58a02836576e32a2d082c262c17"
)

type barkWalletPaths struct {
	Root              string
	DataRoot          string
	WalletDir         string
	AuthDir           string
	ComposePath       string
	CaddyfilePath     string
	TLSDir            string
	AdminPasswordPath string
	SessionSecretPath string
}

type barkWalletApp struct {
	server *Server
}

func newBarkWalletApp(s *Server) appHandler {
	return barkWalletApp{server: s}
}

func barkWalletDefinition() appDefinition {
	return appDefinition{
		ID:          barkWalletAppID,
		Name:        "Bark Wallet",
		Description: "Self-custodial Ark, Lightning, and on-chain wallet powered by Second's public Ark server (beta; does not use local LND).",
		Port:        barkWalletPort,
	}
}

func (a barkWalletApp) Definition() appDefinition {
	return barkWalletDefinition()
}

func (a barkWalletApp) Info(ctx context.Context) (appInfo, error) {
	info := newAppInfo(a.Definition())
	info.Scheme = "https"
	paths := barkWalletAppPaths()
	if !fileExists(paths.ComposePath) {
		return info, nil
	}
	info.Installed = true
	info.AdminPasswordPath = paths.AdminPasswordPath
	status, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, "proxy")
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func (a barkWalletApp) Install(ctx context.Context) error {
	return a.server.installBarkWallet(ctx)
}

func (a barkWalletApp) Uninstall(ctx context.Context) error {
	return a.server.uninstallBarkWallet(ctx)
}

func (a barkWalletApp) Start(ctx context.Context) error {
	return a.server.startBarkWallet(ctx)
}

func (a barkWalletApp) Stop(ctx context.Context) error {
	return a.server.stopBarkWallet(ctx)
}

func barkWalletAppPaths() barkWalletPaths {
	root := filepath.Join(appsRoot, barkWalletAppID)
	dataRoot := filepath.Join(appsDataRoot, barkWalletAppID)
	authDir := filepath.Join(dataRoot, "auth")
	return barkWalletPaths{
		Root:              root,
		DataRoot:          dataRoot,
		WalletDir:         filepath.Join(dataRoot, "wallet"),
		AuthDir:           authDir,
		ComposePath:       filepath.Join(root, "docker-compose.yaml"),
		CaddyfilePath:     filepath.Join(root, "Caddyfile"),
		TLSDir:            filepath.Join(root, "tls"),
		AdminPasswordPath: filepath.Join(authDir, "ui_password"),
		SessionSecretPath: filepath.Join(authDir, "ui_session_secret"),
	}
}

func (s *Server) installBarkWallet(ctx context.Context) error {
	if err := ensureDocker(ctx); err != nil {
		return err
	}
	return s.startBarkWalletPrepared(ctx)
}

func (s *Server) startBarkWallet(ctx context.Context) error {
	if err := ensureDocker(ctx); err != nil {
		return err
	}
	return s.startBarkWalletPrepared(ctx)
}

func (s *Server) startBarkWalletPrepared(ctx context.Context) error {
	paths := barkWalletAppPaths()
	if err := ensureBarkWalletPaths(paths); err != nil {
		return err
	}
	if err := ensureBarkWalletSecrets(paths); err != nil {
		return err
	}
	if err := ensureBarkWalletProxyAssets(paths); err != nil {
		return err
	}
	if err := ensureBarkWalletImages(ctx); err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, barkWalletComposeContents(paths)); err != nil {
		return err
	}
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
		return err
	}
	if err := ensureBarkWalletUFWAccess(ctx); err != nil && s.logger != nil {
		s.logger.Printf("bark wallet: ufw rule failed: %v", err)
	}
	return nil
}

func (s *Server) uninstallBarkWallet(ctx context.Context) error {
	paths := barkWalletAppPaths()
	if fileExists(paths.ComposePath) {
		_ = runCompose(ctx, paths.Root, paths.ComposePath, "down", "--remove-orphans")
	}
	// Wallet data, mnemonic material, authentication secrets, and in-flight
	// off-chain state are intentionally preserved. Destructive wallet deletion
	// belongs to Bark Wallet's own explicit flow, not the generic App Store
	// uninstall action.
	if err := os.RemoveAll(paths.Root); err != nil {
		return fmt.Errorf("failed to remove Bark Wallet app files: %w", err)
	}
	return nil
}

func (s *Server) stopBarkWallet(ctx context.Context) error {
	paths := barkWalletAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("Bark Wallet is not installed")
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "stop")
}

func ensureBarkWalletPaths(paths barkWalletPaths) error {
	for _, dir := range []string{paths.Root, paths.DataRoot, paths.WalletDir, paths.AuthDir, paths.TLSDir} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("failed to create Bark Wallet directory %s: %w", dir, err)
		}
	}
	return nil
}

func ensureBarkWalletSecrets(paths barkWalletPaths) error {
	if readSecretFile(paths.AdminPasswordPath) == "" {
		password, err := randomToken(24)
		if err != nil {
			return fmt.Errorf("failed to generate Bark Wallet UI password: %w", err)
		}
		if err := writeBarkWalletSecret(paths.AdminPasswordPath, password); err != nil {
			return err
		}
	}
	if readSecretFile(paths.SessionSecretPath) == "" {
		secret, err := randomToken(32)
		if err != nil {
			return fmt.Errorf("failed to generate Bark Wallet session secret: %w", err)
		}
		if err := writeBarkWalletSecret(paths.SessionSecretPath, secret); err != nil {
			return err
		}
	}
	for _, path := range []string{paths.AdminPasswordPath, paths.SessionSecretPath} {
		if err := os.Chmod(path, 0600); err != nil {
			return fmt.Errorf("failed to secure Bark Wallet secret %s: %w", path, err)
		}
	}
	return nil
}

func writeBarkWalletSecret(path, value string) error {
	if err := writeFile(path, value+"\n", 0600); err != nil {
		return err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("failed to secure Bark Wallet secret %s: %w", path, err)
	}
	return nil
}

func (s *Server) resetBarkWalletAdminPassword(_ context.Context) error {
	paths := barkWalletAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("Bark Wallet is not installed")
	}
	password, err := randomToken(24)
	if err != nil {
		return fmt.Errorf("failed to generate Bark Wallet UI password: %w", err)
	}
	// bark-web-api reads the password file for every verification. Replacing it
	// invalidates existing signed sessions without exposing barkd or restarting
	// the wallet daemon.
	return writeBarkWalletSecret(paths.AdminPasswordPath, password)
}

func barkWalletComposeContents(paths barkWalletPaths) string {
	return fmt.Sprintf(`services:
  web:
    image: %s
    restart: unless-stopped
    depends_on:
      - api
      - barkd
  api:
    image: %s
    restart: unless-stopped
    stop_grace_period: 1m
    environment:
      PORT: "4001"
      WALLET_DIR: /wallet-data/.bark
      WALLET_DATA_PATH: %s/.bark/
      BARKD_URL: http://barkd:4000
      ARK_SERVER: https://ark.second.tech
      CHAIN_SOURCE: https://mempool.second.tech/api
      BARK_NETWORK: mainnet
      UI_AUTH: "true"
      UI_PASSWORD_FILE: /auth/ui_password
      UI_SESSION_SECRET_FILE: /auth/ui_session_secret
    volumes:
      - %s:/wallet-data:ro
      - %s:/auth:ro
    depends_on:
      - barkd
  barkd:
    image: %s
    user: "0:0"
    restart: unless-stopped
    stop_grace_period: 1m
    command: >-
      sh -c "mkdir -p /data/.bark && chown -R 1000:1000 /data && exec setpriv --reuid 1000 --regid 1000 --clear-groups /usr/local/bin/barkd --port 4000 --host 0.0.0.0 --datadir /data/.bark"
    volumes:
      - %s:/data
  proxy:
    image: %s
    restart: unless-stopped
    depends_on:
      - web
      - api
      - barkd
    ports:
      - "%d:%d"
    volumes:
      - %s:/etc/caddy/Caddyfile:ro
      - %s:/etc/caddy/tls:ro
      - caddy-data:/data
      - caddy-config:/config
volumes:
  caddy-data:
  caddy-config:
`, barkWalletWebImage, barkWalletAPIImage, paths.WalletDir, paths.WalletDir, paths.AuthDir,
		barkWalletDaemonImage, paths.WalletDir, barkWalletProxyImage, barkWalletPort,
		barkWalletPort, paths.CaddyfilePath, paths.TLSDir)
}

func barkWalletCaddyfileContents() string {
	// Route the protected API directly instead of passing it through bark-web's
	// nginx. The upstream nginx derives X-Forwarded-Proto from its own HTTP hop;
	// that would make bark-web-api issue a non-Secure session cookie even though
	// the browser connected over HTTPS. Caddy also handles the ticket-authenticated
	// barkd WebSocket directly, matching the upstream nginx path semantics.
	return fmt.Sprintf(`{
	admin off
	auto_https off
}

https://:%d {
	tls /etc/caddy/tls/server.crt /etc/caddy/tls/server.key

	handle /api/* {
		reverse_proxy api:4001
	}

	handle_path /barkd-ws/* {
		reverse_proxy barkd:4000
	}

	handle {
		reverse_proxy web:8080
	}
}
`, barkWalletPort)
}

func ensureBarkWalletProxyAssets(paths barkWalletPaths) error {
	if err := ensureBarkWalletProxyCert(paths); err != nil {
		return err
	}
	_, err := ensureFileWithChange(paths.CaddyfilePath, barkWalletCaddyfileContents())
	return err
}

func ensureBarkWalletProxyCert(paths barkWalletPaths) error {
	crtPath := filepath.Join(paths.TLSDir, "server.crt")
	keyPath := filepath.Join(paths.TLSDir, "server.key")
	if fileExists(crtPath) && fileExists(keyPath) {
		if err := os.Chmod(keyPath, 0600); err != nil {
			return fmt.Errorf("failed to secure Bark Wallet proxy key: %w", err)
		}
		return os.Chmod(crtPath, 0644)
	}
	if err := os.MkdirAll(paths.TLSDir, 0750); err != nil {
		return fmt.Errorf("failed to create Bark Wallet TLS directory: %w", err)
	}
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("failed to generate Bark Wallet proxy key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("failed to generate Bark Wallet certificate serial: %w", err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "lightningos-bark-wallet"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("failed to create Bark Wallet proxy certificate: %w", err)
	}
	if err := writePEMFile(crtPath, "CERTIFICATE", der, 0644); err != nil {
		return err
	}
	if err := writePEMFile(keyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(priv), 0600); err != nil {
		return err
	}
	if err := os.Chmod(crtPath, 0644); err != nil {
		return fmt.Errorf("failed to set Bark Wallet certificate permissions: %w", err)
	}
	if err := os.Chmod(keyPath, 0600); err != nil {
		return fmt.Errorf("failed to secure Bark Wallet proxy key: %w", err)
	}
	return nil
}

func ensureBarkWalletImages(ctx context.Context) error {
	images := []string{barkWalletWebImage, barkWalletAPIImage, barkWalletDaemonImage, barkWalletProxyImage}
	for _, image := range images {
		if err := ensureDockerImage(ctx, image); err != nil {
			return err
		}
	}
	return nil
}

func ensureBarkWalletUFWAccess(ctx context.Context) error {
	statusOut, err := system.RunCommandWithSudo(ctx, "ufw", "status")
	if err != nil || !strings.Contains(strings.ToLower(statusOut), "status: active") {
		return nil
	}
	_, err = system.RunCommandWithSudo(ctx, "ufw", "allow", fmt.Sprintf("%d/tcp", barkWalletPort))
	return err
}
