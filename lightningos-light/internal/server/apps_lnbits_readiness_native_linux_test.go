package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/privileged"
)

// Called only by the opt-in, root, MAC-independent regtest Docker fixture.
// This never runs in the general unit suite or on a production installation.
func validateLNbitsNativeStartupReadiness(t *testing.T, ctx context.Context, broker *privileged.ComposeAppManager, paths lnbitsPaths, waitApp func()) {
	t.Helper()
	run := func(name string, args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		if err != nil {
			// Docker diagnostics can contain runtime/credential material.
			_ = os.WriteFile(filepath.Join(filepath.Dir(broker.PrivilegedAppsRoot), "readiness-command-error.log"), out, 0600)
			t.Fatalf("isolated readiness command failed: %s %s (%v; private diagnostics retained)", filepath.Base(name), strings.Join(args[:min(2, len(args))], " "), err)
		}
		return string(out)
	}
	if strings.TrimSpace(run("docker", "ps", "-aq", "--filter", "name=^lnbits-baseline$")) != "" {
		t.Fatal("baseline container name is already occupied; existing container preserved")
	}
	lifecycle := func(action privileged.AppLifecycleAction) {
		t.Helper()
		if err := broker.Lifecycle(ctx, appmanifest.LNbitsID, action, false); err != nil {
			t.Fatalf("readiness lifecycle %s: %v", action, err)
		}
	}
	state := func() string {
		return strings.TrimSpace(run("docker", "inspect", "--format", "{{.State.Status}}", "lnbits"))
	}
	waitLog := func(container, needle string, budget time.Duration) {
		t.Helper()
		until := time.Now().Add(budget)
		for time.Now().Before(until) {
			if strings.Contains(strings.ToLower(run("docker", "logs", container)), strings.ToLower(needle)) {
				return
			}
			time.Sleep(time.Second)
		}
		t.Fatalf("readiness log marker not reached for %s (logs retained privately)", container)
	}
	lifecycle(privileged.AppLifecycleAction("stop"))
	// Material is already limited to this test's dedicated macaroon/certificate.
	snapshot := filepath.Join(broker.PrivilegedAppsRoot, "lnbits")
	raw, err := os.ReadFile(filepath.Join(snapshot, appmanifest.LNbitsComposeFile))
	if err != nil {
		t.Fatal(err)
	}
	start, end := strings.Index(string(raw), "    command:\n"), strings.Index(string(raw), "    tmpfs:\n")
	if start < 0 || end <= start {
		t.Fatal("candidate startup command unavailable")
	}
	old := string(raw[:start]) + "    command: [/app/.venv/bin/lnbits, --port, '5000', --host, 0.0.0.0, '--forwarded-allow-ips=*']\n" + string(raw[end:])
	old = strings.Replace(old, "container_name: lnbits", "container_name: lnbits-baseline", 1)
	old = strings.Replace(old, "./.env", filepath.Join(snapshot, appmanifest.LNbitsEnvFile), 1)
	baseline := filepath.Join(filepath.Dir(broker.PrivilegedAppsRoot), "baseline-compose.yaml")
	if err := os.WriteFile(baseline, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	// Always restore the single regtest service and remove only our baseline
	// container; never remove volumes or native regtest data.
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := exec.CommandContext(c, "systemctl", "start", "los-regtest-alice").Run(); err != nil {
			t.Error("restore regtest Alice failed")
		}
		_ = exec.CommandContext(c, "docker", "rm", "-f", "lnbits-baseline").Run()
	})
	run("systemctl", "stop", "los-regtest-alice")
	run("docker", "compose", "--project-name", "los0557-baseline", "-f", baseline, "up", "-d", "--no-build")
	waitLog("lnbits-baseline", "VoidWallet", 150*time.Second)
	run("docker", "rm", "-f", "lnbits-baseline")
	t.Log("reproduced original pinned image fallback with unavailable regtest LND")
	lifecycle(privileged.AppLifecycleAction("start"))
	waitLog("lnbits", "waiting for authenticated LND funding RPC", 30*time.Second)
	time.Sleep(35 * time.Second)
	if state() != "running" {
		t.Fatal("gate did not retain a running wait process")
	}
	if strings.Contains(run("docker", "logs", "lnbits"), "VoidWallet") {
		t.Fatal("candidate launched upstream before LND readiness")
	}
	lifecycle(privileged.AppLifecycleAction("stop"))
	if state() != "exited" {
		t.Fatal("operator Stop while waiting was not respected")
	}
	run("systemctl", "restart", "docker")
	if state() != "exited" {
		t.Fatal("manually stopped container restarted with Docker")
	}
	lifecycle(privileged.AppLifecycleAction("start"))
	run("systemctl", "restart", "docker")
	until := time.Now().Add(30 * time.Second)
	for state() != "running" && time.Now().Before(until) {
		time.Sleep(time.Second)
	}
	if state() != "running" {
		t.Fatal("unless-stopped autostart failed")
	}
	run("systemctl", "start", "los-regtest-alice")
	waitApp()
	waitLog("lnbits", "LND funding RPC ready; starting LNbits", 30*time.Second)
	// The waiting process must be replaced, not left as a runtime watchdog.
	// docker-init retains the original gate command in its argv after exec.
	// Inspect its child, not init's unchanged command-line metadata.
	run("docker", "exec", "lnbits", "/app/.venv/bin/python", "-I", "-c", `from pathlib import Path
children=Path('/proc/1/task/1/children').read_text().split()
assert len(children)==1, 'unexpected init children'
argv=Path('/proc/'+children[0]+'/cmdline').read_bytes().split(b'\0')
assert b'/app/.venv/bin/lnbits' in argv, 'LNbits exec target missing'
assert b'-c' not in argv, 'startup gate remains active'
`)
	t.Log("candidate waits beyond old budget, Stop while waiting, Docker autostart/stopped persistence, recovery and exec passed; payment/security suite follows")
}
