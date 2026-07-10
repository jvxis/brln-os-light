package server

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBarkWalletDefinition(t *testing.T) {
	def := barkWalletDefinition()
	if def.ID != barkWalletAppID {
		t.Fatalf("unexpected id: %s", def.ID)
	}
	if def.Name != "Bark Wallet" {
		t.Fatalf("unexpected name: %s", def.Name)
	}
	if def.Port != barkWalletPort {
		t.Fatalf("unexpected port: %d", def.Port)
	}
}

func TestBarkWalletComposeContentsMatchesHardenedUmbrelLayout(t *testing.T) {
	paths := barkWalletPaths{
		WalletDir:     "/tmp/bark-wallet/wallet",
		AuthDir:       "/tmp/bark-wallet/auth",
		CaddyfilePath: "/tmp/bark-wallet-app/Caddyfile",
		TLSDir:        "/tmp/bark-wallet-app/tls",
	}
	got := barkWalletComposeContents(paths)

	wants := []string{
		"image: " + barkWalletWebImage,
		"image: " + barkWalletAPIImage,
		"image: " + barkWalletDaemonImage,
		"image: " + barkWalletProxyImage,
		"ARK_SERVER: https://ark.second.tech",
		"CHAIN_SOURCE: https://mempool.second.tech/api",
		"BARK_NETWORK: mainnet",
		"UI_AUTH: \"true\"",
		"UI_PASSWORD_FILE: /auth/ui_password",
		"UI_SESSION_SECRET_FILE: /auth/ui_session_secret",
		"- /tmp/bark-wallet/wallet:/wallet-data:ro",
		"- /tmp/bark-wallet/auth:/auth:ro",
		"- /tmp/bark-wallet/wallet:/data",
		"- /tmp/bark-wallet-app/Caddyfile:/etc/caddy/Caddyfile:ro",
		"- /tmp/bark-wallet-app/tls:/etc/caddy/tls:ro",
		"setpriv --reuid 1000 --regid 1000 --clear-groups",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("compose output missing %q\n%s", want, got)
		}
	}

	if strings.Contains(got, "/data/lnd") || strings.Contains(got, "macaroon") {
		t.Fatalf("Bark Wallet must not mount or reference local LND credentials:\n%s", got)
	}
	if strings.Contains(got, ":latest") {
		t.Fatalf("Bark Wallet images must be pinned by version and digest:\n%s", got)
	}
	if strings.Count(got, `- "4004:4004"`) != 1 {
		t.Fatalf("expected only the TLS proxy to publish port 4004:\n%s", got)
	}
	if strings.Contains(got, `"4000:4000"`) || strings.Contains(got, `"4001:4001"`) || strings.Contains(got, `"8080:8080"`) {
		t.Fatalf("web, API, and barkd ports must remain internal:\n%s", got)
	}
}

func TestBarkWalletCaddyfileServesTLS(t *testing.T) {
	got := barkWalletCaddyfileContents()
	for _, want := range []string{
		"https://:4004",
		"tls /etc/caddy/tls/server.crt /etc/caddy/tls/server.key",
		"handle /api/*",
		"reverse_proxy api:4001",
		"handle_path /barkd-ws/*",
		"reverse_proxy barkd:4000",
		"reverse_proxy web:8080",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Caddyfile missing %q\n%s", want, got)
		}
	}
}

func TestEnsureBarkWalletSecretsPreservesExistingValues(t *testing.T) {
	dir := t.TempDir()
	paths := barkWalletPaths{
		AuthDir:           filepath.Join(dir, "auth"),
		AdminPasswordPath: filepath.Join(dir, "auth", "ui_password"),
		SessionSecretPath: filepath.Join(dir, "auth", "ui_session_secret"),
	}
	if err := os.MkdirAll(paths.AuthDir, 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := ensureBarkWalletSecrets(paths); err != nil {
		t.Fatalf("ensure secrets: %v", err)
	}
	password := readSecretFile(paths.AdminPasswordPath)
	secret := readSecretFile(paths.SessionSecretPath)
	if password == "" || secret == "" || password == secret {
		t.Fatalf("expected distinct non-empty secrets")
	}
	if err := ensureBarkWalletSecrets(paths); err != nil {
		t.Fatalf("ensure secrets again: %v", err)
	}
	if got := readSecretFile(paths.AdminPasswordPath); got != password {
		t.Fatalf("password changed on idempotent ensure")
	}
	if got := readSecretFile(paths.SessionSecretPath); got != secret {
		t.Fatalf("session secret changed on idempotent ensure")
	}
	for _, path := range []string{paths.AdminPasswordPath, paths.SessionSecretPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
			t.Fatalf("unexpected mode for %s: %o", path, info.Mode().Perm())
		}
	}
}

func TestEnsureBarkWalletProxyCertIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	paths := barkWalletPaths{TLSDir: filepath.Join(dir, "tls")}
	if err := ensureBarkWalletProxyCert(paths); err != nil {
		t.Fatalf("ensure certificate: %v", err)
	}
	crtPath := filepath.Join(paths.TLSDir, "server.crt")
	keyPath := filepath.Join(paths.TLSDir, "server.key")
	crt, err := os.ReadFile(crtPath)
	if err != nil {
		t.Fatalf("read certificate: %v", err)
	}
	if !strings.Contains(string(crt), "BEGIN CERTIFICATE") {
		t.Fatalf("certificate is not PEM")
	}
	keyInfo, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat private key: %v", err)
	}
	if runtime.GOOS != "windows" && keyInfo.Mode().Perm() != 0600 {
		t.Fatalf("unexpected private key mode: %o", keyInfo.Mode().Perm())
	}
	if err := ensureBarkWalletProxyCert(paths); err != nil {
		t.Fatalf("ensure certificate again: %v", err)
	}
	crtAgain, err := os.ReadFile(crtPath)
	if err != nil {
		t.Fatalf("read certificate again: %v", err)
	}
	if string(crt) != string(crtAgain) {
		t.Fatalf("certificate changed on idempotent ensure")
	}
}
