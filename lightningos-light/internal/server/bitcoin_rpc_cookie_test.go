package server

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeTestCookie(t *testing.T, content string) string {
	t.Helper()
	return writeTestFile(t, ".cookie", content)
}

func writeTestFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseBitcoindRPCConfigFromLNDConfUsesRPCCookie(t *testing.T) {
	cookie := writeTestCookie(t, "__cookie__:s3cret\n")
	raw := "[Bitcoind]\nbitcoind.rpchost=127.0.0.1:8332\nbitcoind.rpccookie=" + cookie + "\n"

	cfg, ok := parseBitcoindRPCConfigFromLNDConf(raw)
	if !ok {
		t.Fatalf("expected cookie-authenticated config")
	}
	if cfg.User != "__cookie__" || cfg.Pass != "s3cret" {
		t.Fatalf("unexpected cookie credentials: %q/%q", cfg.User, cfg.Pass)
	}
}

func TestParseBitcoindRPCConfigFromLNDConfPrefersExplicitCredentialsOverCookie(t *testing.T) {
	cookie := writeTestCookie(t, "__cookie__:s3cret")
	raw := "[Bitcoind]\nbitcoind.rpccookie=" + cookie + "\nbitcoind.rpcuser=user\nbitcoind.rpcpass=pass\n"

	cfg, ok := parseBitcoindRPCConfigFromLNDConf(raw)
	if !ok || cfg.User != "user" || cfg.Pass != "pass" {
		t.Fatalf("expected explicit credentials, got %+v ok=%v", cfg, ok)
	}
}

func TestParseBitcoindRPCConfigFromLNDConfRejectsUnsafeCookie(t *testing.T) {
	secret := writeTestFile(t, "secrets.env", "LND_PG_DSN=postgres://user:pass@127.0.0.1/lnd\n")
	for name, path := range map[string]string{
		"missing":       filepath.Join(t.TempDir(), ".cookie"),
		"malformed":     writeTestCookie(t, "no-separator"),
		"empty":         writeTestCookie(t, "__cookie__:"),
		"not a .cookie": secret,
		"oversized":     writeTestCookie(t, "__cookie__:"+strings.Repeat("a", 2048)),
	} {
		raw := "[Bitcoind]\nbitcoind.rpccookie=" + path + "\n"
		if cfg, ok := parseBitcoindRPCConfigFromLNDConf(raw); ok {
			t.Fatalf("%s cookie: expected no config, got %+v", name, cfg)
		}
	}
	t.Run("symlink", func(t *testing.T) {
		symlink := filepath.Join(t.TempDir(), ".cookie")
		if err := os.Symlink(secret, symlink); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("symlink fixture unavailable: %v", err)
			}
			t.Fatal(err)
		}
		raw := "[Bitcoind]\nbitcoind.rpccookie=" + symlink + "\n"
		if _, ok := parseBitcoindRPCConfigFromLNDConf(raw); ok {
			t.Fatal("symlink cookie must be rejected")
		}
	})
}

func TestParseBitcoindRPCConfigFromLNDConfIgnoresCookieForRemoteHost(t *testing.T) {
	cookie := writeTestCookie(t, "__cookie__:s3cret")
	raw := "[Bitcoind]\nbitcoind.rpchost=attacker.example:8332\nbitcoind.rpccookie=" + cookie + "\n"

	if cfg, ok := parseBitcoindRPCConfigFromLNDConf(raw); ok {
		t.Fatalf("cookie credentials must not be sent to a remote host, got %+v", cfg)
	}
}
