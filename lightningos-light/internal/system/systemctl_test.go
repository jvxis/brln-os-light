//go:build !windows

package system

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSystemctlRestartNoBlockFallsBackToSystemdRun(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	systemctlPath := filepath.Join(dir, "systemctl")
	sudoPath := filepath.Join(dir, "sudo")
	systemdRunPath := filepath.Join(dir, "systemd-run")

	writeExecutable(t, systemctlPath, `#!/bin/sh
printf '%s\n' "systemctl $*" >> "$LIGHTNINGOS_TEST_LOG"
exit 1
`)
	writeExecutable(t, sudoPath, `#!/bin/sh
printf '%s\n' "sudo $*" >> "$LIGHTNINGOS_TEST_LOG"
if [ "$2" = "$LIGHTNINGOS_SYSTEMD_RUN" ]; then
  exit 0
fi
exit 1
`)
	writeExecutable(t, systemdRunPath, `#!/bin/sh
exit 0
`)

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LIGHTNINGOS_TEST_LOG", logPath)
	t.Setenv("LIGHTNINGOS_SYSTEMD_RUN", systemdRunPath)

	if err := SystemctlRestartNoBlock(context.Background(), "lnd"); err != nil {
		t.Fatalf("expected systemd-run fallback to succeed: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	calls := string(data)
	expectedDirect := "systemctl restart --no-block lnd"
	if !strings.Contains(calls, expectedDirect) {
		t.Fatalf("expected direct systemctl call %q, got:\n%s", expectedDirect, calls)
	}
	expectedSudo := "sudo -n " + systemctlPath + " restart --no-block lnd"
	if !strings.Contains(calls, expectedSudo) {
		t.Fatalf("expected sudo systemctl call %q, got:\n%s", expectedSudo, calls)
	}
	fallbackPattern := regexp.QuoteMeta("sudo -n "+systemdRunPath+" --quiet --collect --unit lightningos-restart-lnd-") +
		`[0-9]+` +
		regexp.QuoteMeta(" "+systemctlPath+" restart --no-block lnd")
	if !regexp.MustCompile(fallbackPattern).MatchString(calls) {
		t.Fatalf("expected sudo systemd-run fallback matching %q, got:\n%s", fallbackPattern, calls)
	}
}

func writeExecutable(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
