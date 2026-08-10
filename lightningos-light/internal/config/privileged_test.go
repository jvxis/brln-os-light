package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPrivilegedDefaults(t *testing.T) {
	configPath := writeMinimalConfig(t, "")
	t.Setenv("LIGHTNINGOS_PRIVILEGED_MODE", "")
	t.Setenv("LIGHTNINGOS_PRIVILEGED_TIMEOUT_SECONDS", "")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Privileged.Mode != "disabled" || cfg.Privileged.TimeoutSeconds != 5 {
		t.Fatalf("unexpected defaults: %#v", cfg.Privileged)
	}
}

func TestLoadPrivilegedOverrides(t *testing.T) {
	configPath := writeMinimalConfig(t, "privileged:\n  mode: disabled\n  timeout_seconds: 4\n")
	t.Setenv("LIGHTNINGOS_PRIVILEGED_MODE", "SHADOW")
	t.Setenv("LIGHTNINGOS_PRIVILEGED_TIMEOUT_SECONDS", "9")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Privileged.Mode != "shadow" || cfg.Privileged.TimeoutSeconds != 9 {
		t.Fatalf("unexpected overrides: %#v", cfg.Privileged)
	}
}

func TestLoadRejectsInvalidPrivilegedConfig(t *testing.T) {
	t.Setenv("LIGHTNINGOS_PRIVILEGED_MODE", "root-shell")
	if _, err := Load(writeMinimalConfig(t, "")); err == nil || !strings.Contains(err.Error(), "privileged.mode") {
		t.Fatalf("expected invalid mode error, got %v", err)
	}

	t.Setenv("LIGHTNINGOS_PRIVILEGED_MODE", "enforce")
	t.Setenv("LIGHTNINGOS_PRIVILEGED_TIMEOUT_SECONDS", "31")
	if _, err := Load(writeMinimalConfig(t, "")); err == nil || !strings.Contains(err.Error(), "timeout_seconds") {
		t.Fatalf("expected invalid timeout error, got %v", err)
	}
}

func writeMinimalConfig(t *testing.T, extra string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := "server:\n  tls_cert: /tmp/test.crt\n  tls_key: /tmp/test.key\n" + extra
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
