package privileged

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/appmanifest"
)

type composeRecordingRunner struct {
	commands        []recordedCommand
	composeSnapshot string
	envSnapshot     string
	standalone      bool
	runningServices string
	containerID     string
	cpuPercent      string
	statsErr        error
	imageErr        error
	hook            func(path string, args []string) (string, error, bool)
}

func (runner *composeRecordingRunner) Run(_ context.Context, path string, args ...string) (string, error) {
	runner.commands = append(runner.commands, recordedCommand{path: path, args: append([]string(nil), args...)})
	if runner.hook != nil {
		if output, err, handled := runner.hook(path, args); handled {
			return output, err
		}
	}
	if path == dockerPath && reflect.DeepEqual(args, []string{"compose", "version"}) {
		if runner.standalone {
			return "", errors.New("compose plugin unavailable")
		}
		return "Docker Compose version v2", nil
	}
	if path == dockerComposePath && reflect.DeepEqual(args, []string{"version"}) {
		return "docker-compose version 1", nil
	}
	if path == dockerPath && len(args) == 3 && args[0] == "image" && args[1] == "inspect" {
		return "", runner.imageErr
	}
	for index, arg := range args {
		if arg == "-f" && index+1 < len(args) {
			raw, _ := os.ReadFile(args[index+1])
			runner.composeSnapshot = string(raw)
		}
		if arg == "--env-file" && index+1 < len(args) {
			raw, _ := os.ReadFile(args[index+1])
			runner.envSnapshot = string(raw)
		}
	}
	if hasArgsSuffix(args, "ps", "--services", "--filter", "status=running") {
		return runner.runningServices, nil
	}
	if hasArgsSuffix(args, "ps", "-q", appmanifest.CPUMinerID) {
		return runner.containerID, nil
	}
	if path == dockerPath && len(args) == 5 && reflect.DeepEqual(args[:4], []string{"stats", "--no-stream", "--format", "{{.CPUPerc}}"}) {
		return runner.cpuPercent, runner.statsErr
	}
	return "", nil
}

func TestComposeAppEnsureDockerRuntimeUsesFixedCommands(t *testing.T) {
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner}
	state, err := manager.EnsureDockerRuntime(context.Background(), false)
	if err != nil || state.Status != "ready" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	want := []recordedCommand{
		{path: dockerPath, args: []string{"info"}},
		{path: dockerPath, args: []string{"compose", "version"}},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands=%#v want=%#v", runner.commands, want)
	}
}

func TestComposeAppEnsureDockerRuntimeStartsInstalledDaemon(t *testing.T) {
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == dockerPath && reflect.DeepEqual(args, []string{"info"}) {
			return "", errors.New("inactive"), true
		}
		if path == systemctlPath && reflect.DeepEqual(args, []string{"is-active", "docker"}) {
			return "inactive\n", errors.New("inactive"), true
		}
		return "", nil, false
	}}
	manager := &ComposeAppManager{Runner: runner}
	state, err := manager.EnsureDockerRuntime(context.Background(), false)
	if err != nil || state.Status != "starting" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	if len(runner.commands) != 3 || runner.commands[2].path != systemctlPath || !reflect.DeepEqual(runner.commands[2].args, []string{"start", "--no-block", "docker"}) {
		t.Fatalf("unexpected runtime command sequence: %#v", runner.commands)
	}
}

func TestComposeAppEnsureDockerRuntimeDryRunExecutesNothing(t *testing.T) {
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner}
	state, err := manager.EnsureDockerRuntime(context.Background(), true)
	if err != nil || state.Status != "validated" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("dry run executed commands: %#v", runner.commands)
	}
}

func TestComposeAppEnsureFirewallAccessUsesFixedUFWCommands(t *testing.T) {
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == ufwPath && reflect.DeepEqual(args, []string{"status"}) {
			return "Status: active\n", nil, true
		}
		return "", nil, false
	}}
	manager := &ComposeAppManager{Runner: runner}
	state, err := manager.EnsureFirewallAccess(context.Background(), appmanifest.RoboSatsID, false)
	if err != nil || state.Status != "active" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	want := []recordedCommand{
		{path: ufwPath, args: []string{"status"}},
		{path: ufwPath, args: []string{"allow", "12596/tcp"}},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands=%#v want=%#v", runner.commands, want)
	}
}

func TestComposeAppEnsureFirewallAccessInactiveUnavailableAndDryRun(t *testing.T) {
	for _, test := range []struct {
		name       string
		output     string
		runErr     error
		dryRun     bool
		wantStatus string
		wantCalls  int
	}{
		{name: "inactive", output: "Status: inactive\n", wantStatus: "inactive", wantCalls: 1},
		{name: "unavailable", runErr: errors.New("missing"), wantStatus: "unavailable", wantCalls: 1},
		{name: "dry run", dryRun: true, wantStatus: "validated"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
				if path == ufwPath {
					return test.output, test.runErr, true
				}
				return "", nil, false
			}}
			manager := &ComposeAppManager{Runner: runner}
			state, err := manager.EnsureFirewallAccess(context.Background(), appmanifest.RoboSatsID, test.dryRun)
			if err != nil || state.Status != test.wantStatus || len(runner.commands) != test.wantCalls {
				t.Fatalf("state/error/commands=%#v/%v/%#v", state, err, runner.commands)
			}
		})
	}

	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner}
	if _, err := manager.EnsureFirewallAccess(context.Background(), "robosats;reboot", false); err == nil || len(runner.commands) != 0 {
		t.Fatalf("untrusted app was not rejected before execution: %#v", runner.commands)
	}
}

func TestComposeAppPrepareImageSchedulesFixedTransientPull(t *testing.T) {
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == dockerPath && len(args) == 3 && args[0] == "image" && args[1] == "inspect" {
			return "", errors.New("missing"), true
		}
		if path == systemctlPath && len(args) > 0 && args[0] == "show" {
			return "LoadState=not-found\nActiveState=inactive\n", errors.New("not found"), true
		}
		return "", nil, false
	}}
	manager := &ComposeAppManager{Runner: runner}
	state, err := manager.PrepareImage(context.Background(), "cpuminer", appmanifest.CPUMinerImageBaseline, false)
	if err != nil || state.Status != "preparing" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	if len(runner.commands) != 3 {
		t.Fatalf("commands=%#v", runner.commands)
	}
	want := recordedCommand{path: systemdRunPath, args: []string{
		"--quiet", "--collect", "--unit=lightningos-cpuminer-image-baseline",
		"--property=Type=exec", "--property=RuntimeMaxSec=10min",
		dockerPath, "pull", "jvx1971/cpu-lottery-miner:v1",
	}}
	if !reflect.DeepEqual(runner.commands[2], want) {
		t.Fatalf("pull command=%#v want=%#v", runner.commands[2], want)
	}
}

func TestComposeAppPrepareRoboSatsImagesUsesFixedTransientPulls(t *testing.T) {
	for _, test := range []struct {
		variant appmanifest.AppImageVariant
		image   string
		unit    string
	}{
		{variant: appmanifest.RoboSatsImageClient, image: appmanifest.RoboSatsImage, unit: "lightningos-robosats-image-client"},
		{variant: appmanifest.RoboSatsImageTor, image: appmanifest.RoboSatsTorImage, unit: "lightningos-robosats-image-tor"},
		{variant: appmanifest.RoboSatsImageProxy, image: appmanifest.RoboSatsProxyImage, unit: "lightningos-robosats-image-proxy"},
	} {
		t.Run(string(test.variant), func(t *testing.T) {
			runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
				if path == dockerPath && len(args) == 3 && args[0] == "image" && args[1] == "inspect" {
					return "", errors.New("missing"), true
				}
				if path == systemctlPath && len(args) > 0 && args[0] == "show" {
					return "LoadState=not-found\nActiveState=inactive\n", errors.New("not found"), true
				}
				return "", nil, false
			}}
			manager := &ComposeAppManager{Runner: runner}
			state, err := manager.PrepareImage(context.Background(), appmanifest.RoboSatsID, test.variant, false)
			if err != nil || state.Status != "preparing" || len(runner.commands) != 3 {
				t.Fatalf("state=%#v err=%v commands=%#v", state, err, runner.commands)
			}
			want := recordedCommand{path: systemdRunPath, args: []string{
				"--quiet", "--collect", "--unit=" + test.unit,
				"--property=Type=exec", "--property=RuntimeMaxSec=10min",
				dockerPath, "pull", test.image,
			}}
			if !reflect.DeepEqual(runner.commands[2], want) {
				t.Fatalf("pull command=%#v want=%#v", runner.commands[2], want)
			}
		})
	}
}

func TestComposeAppPrepareBitcoinCoreImageUsesVerifiedLocalBuild(t *testing.T) {
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == systemctlPath && len(args) > 0 && args[0] == "show" {
			return "LoadState=not-found\nActiveState=inactive\n", errors.New("not found"), true
		}
		return "", nil, false
	}}
	manager := &ComposeAppManager{Runner: runner, PrivilegedAppsRoot: filepath.Join(t.TempDir(), "privileged-apps")}
	state, err := manager.PrepareImage(context.Background(), appmanifest.BitcoinCoreID, appmanifest.BitcoinCoreImageNode, false)
	if err != nil || state.Status != "preparing" || len(runner.commands) != 2 {
		t.Fatalf("state=%#v err=%v commands=%#v", state, err, runner.commands)
	}
	command := runner.commands[1]
	if command.path != systemdRunPath || len(command.args) != 8 || command.args[0] != "--quiet" || command.args[2] != "--unit=lightningos-bitcoincore-image-node" || command.args[5] != "/bin/sh" || command.args[6] != "-c" {
		t.Fatalf("unexpected build command=%#v", command)
	}
	script := command.args[7]
	for _, expected := range []string{
		"https://bitcoincore.org/bin/bitcoin-core-31.1/SHA256SUMS",
		"b80d9c3e04da78fb6f0569685673418cf686fadba9042d926d13fb87ff503f9e",
		"--status-fd 1 --verify",
		"signature_count",
		"-ge 3",
		"docker build --pull --no-cache --network=none",
		"lightningos/bitcoin-core:31.1",
		"image-attestation",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("verified build script missing %q:\n%s", expected, script)
		}
	}
	for _, forbidden := range []string{"docker pull bitcoin/bitcoin", "bitcoin/bitcoin:31.1", "latest"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("verified build script contains %q:\n%s", forbidden, script)
		}
	}
}

func TestComposeAppPrepareImageReturnsCachedOrInProgressState(t *testing.T) {
	for _, test := range []struct {
		name       string
		inspectErr error
		show       string
		want       string
		wantCalls  int
	}{
		{name: "ready", want: "ready", wantCalls: 1},
		{name: "preparing", inspectErr: errors.New("missing"), show: "LoadState=loaded\nActiveState=active\nSubState=running\n", want: "preparing", wantCalls: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
				if path == dockerPath && len(args) == 3 && args[0] == "image" && args[1] == "inspect" {
					return "", test.inspectErr, true
				}
				if path == systemctlPath {
					return test.show, nil, true
				}
				return "", nil, false
			}}
			manager := &ComposeAppManager{Runner: runner}
			state, err := manager.PrepareImage(context.Background(), "cpuminer", appmanifest.CPUMinerImageFastLatest, false)
			if err != nil || state.Status != test.want || len(runner.commands) != test.wantCalls {
				t.Fatalf("state=%#v err=%v commands=%#v", state, err, runner.commands)
			}
		})
	}
}

func TestComposeAppImageStatusAndProbeUseAllowlistedImage(t *testing.T) {
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner}
	state, err := manager.ImageStatus(context.Background(), "cpuminer", appmanifest.CPUMinerImageFastPinned)
	if err != nil || state.Status != "ready" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	probe, err := manager.ProbeImage(context.Background(), "cpuminer", appmanifest.CPUMinerImageFastPinned, false)
	if err != nil || !probe.Runnable {
		t.Fatalf("probe/error=%#v/%v", probe, err)
	}
	image := "cniweb/cpuminer-opt@sha256:8aba97834d6a6e1946b2a61c8939eee8907b7be97d8e77c1174f66579d5bd90b"
	if len(runner.commands) != 3 || !reflect.DeepEqual(runner.commands[2], recordedCommand{path: dockerPath, args: []string{"run", "--rm", image, "cpuminer", "--algo", "sha256d", "--benchmark", "--time-limit", "2"}}) {
		t.Fatalf("unexpected image commands: %#v", runner.commands)
	}
}

func TestComposeAppImageOperationsRejectUntrustedInputsBeforeCommand(t *testing.T) {
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner}
	if _, err := manager.PrepareImage(context.Background(), "mempool", appmanifest.CPUMinerImageBaseline, false); err == nil {
		t.Fatal("expected unknown app to fail")
	}
	if _, err := manager.ImageStatus(context.Background(), "cpuminer", "../../evil"); err == nil {
		t.Fatal("expected unknown image variant to fail")
	}
	if _, err := manager.ProbeImage(context.Background(), "cpuminer", "latest;reboot", false); err == nil {
		t.Fatal("expected injected image variant to fail")
	}
	if _, err := manager.ProbeImage(context.Background(), "robosats", appmanifest.RoboSatsImageClient, false); err == nil {
		t.Fatal("expected RoboSats compatibility probe to fail")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("rejected image input executed commands: %#v", runner.commands)
	}
}

func hasArgsSuffix(args []string, suffix ...string) bool {
	return len(args) >= len(suffix) && reflect.DeepEqual(args[len(args)-len(suffix):], suffix)
}

func TestComposeAppLifecycleUsesValidatedSnapshotAndFixedCommand(t *testing.T) {
	appsRoot, validEnv := writeTestCPUMinerApp(t)
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, TempRoot: t.TempDir()}
	if err := manager.Lifecycle(context.Background(), "cpuminer", AppLifecycleStart, false); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 3 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	if runner.commands[0].path != dockerPath || !reflect.DeepEqual(runner.commands[0].args, []string{"image", "inspect", "jvx1971/cpu-lottery-miner:v1"}) {
		t.Fatalf("unexpected image probe: %#v", runner.commands[0])
	}
	if runner.commands[1].path != dockerPath || !reflect.DeepEqual(runner.commands[1].args, []string{"compose", "version"}) {
		t.Fatalf("unexpected compose probe: %#v", runner.commands[1])
	}
	lifecycle := runner.commands[2]
	if lifecycle.path != dockerPath || len(lifecycle.args) < 11 || lifecycle.args[0] != "compose" || lifecycle.args[len(lifecycle.args)-2] != "up" || lifecycle.args[len(lifecycle.args)-1] != "-d" {
		t.Fatalf("unexpected lifecycle command: %#v", lifecycle)
	}
	if runner.composeSnapshot != appmanifest.CPUMinerCompose() || runner.envSnapshot != validEnv {
		t.Fatal("broker did not execute the validated manifest snapshot")
	}
	for _, arg := range lifecycle.args {
		if strings.Contains(arg, appsRoot) {
			t.Fatalf("manager-writable path reached Docker command: %q", arg)
		}
	}
}

func TestComposeAppLifecycleStartRequiresLocalImage(t *testing.T) {
	appsRoot, _ := writeTestCPUMinerApp(t)
	runner := &composeRecordingRunner{imageErr: errors.New("missing")}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, TempRoot: t.TempDir()}
	if err := manager.Lifecycle(context.Background(), "cpuminer", AppLifecycleStart, false); err == nil {
		t.Fatal("expected missing local image to fail")
	}
	if len(runner.commands) != 1 || !reflect.DeepEqual(runner.commands[0].args, []string{"image", "inspect", "jvx1971/cpu-lottery-miner:v1"}) {
		t.Fatalf("missing image reached Compose: %#v", runner.commands)
	}
}

func TestComposeAppRoboSatsLifecycleUsesBrokerOwnedProxySnapshot(t *testing.T) {
	appsRoot := writeTestRoboSatsApp(t)
	privilegedAppsRoot := t.TempDir()
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, PrivilegedAppsRoot: privilegedAppsRoot}
	if err := manager.Lifecycle(context.Background(), appmanifest.RoboSatsID, AppLifecycleStart, false); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 5 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	for index, image := range appmanifest.RoboSatsImages() {
		want := recordedCommand{path: dockerPath, args: []string{"image", "inspect", image}}
		if !reflect.DeepEqual(runner.commands[index], want) {
			t.Fatalf("image probe %d = %#v want %#v", index, runner.commands[index], want)
		}
	}
	lifecycle := runner.commands[4]
	if lifecycle.path != dockerPath || !hasArgsSuffix(lifecycle.args, "up", "-d") {
		t.Fatalf("unexpected lifecycle command: %#v", lifecycle)
	}
	appRoot := filepath.Join(appsRoot, appmanifest.RoboSatsID)
	if strings.Contains(runner.composeSnapshot, appRoot) {
		t.Fatalf("manager-owned proxy path reached execution manifest:\n%s", runner.composeSnapshot)
	}
	if !strings.Contains(runner.composeSnapshot, "robosats-data:/usr/src/robosats/data") {
		t.Fatalf("execution manifest did not use the named data volume:\n%s", runner.composeSnapshot)
	}
	if !strings.Contains(runner.composeSnapshot, appmanifest.RoboSatsCaddyfileFile+":/etc/caddy/Caddyfile:ro") ||
		!strings.Contains(runner.composeSnapshot, appmanifest.RoboSatsTLSDir+":/etc/caddy/tls:ro") {
		t.Fatalf("broker snapshot proxy mounts missing:\n%s", runner.composeSnapshot)
	}
	for _, arg := range lifecycle.args {
		if strings.Contains(arg, appsRoot) {
			t.Fatalf("manager-controlled path reached Docker arguments: %q", arg)
		}
	}
	if !strings.Contains(runner.composeSnapshot, privilegedAppsRoot) {
		t.Fatalf("execution manifest did not use the persistent privileged root:\n%s", runner.composeSnapshot)
	}
	for _, path := range []string{
		filepath.Join(privilegedAppsRoot, appmanifest.RoboSatsID, appmanifest.RoboSatsComposeFile),
		filepath.Join(privilegedAppsRoot, appmanifest.RoboSatsID, appmanifest.RoboSatsCaddyfileFile),
		filepath.Join(privilegedAppsRoot, appmanifest.RoboSatsID, appmanifest.RoboSatsTLSDir, "server.crt"),
		filepath.Join(privilegedAppsRoot, appmanifest.RoboSatsID, appmanifest.RoboSatsTLSDir, "server.key"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("persistent broker asset missing after lifecycle call: %s: %v", path, err)
		}
	}
}

func TestComposeAppRoboSatsInspectUsesPrimaryServiceWithoutDockerStats(t *testing.T) {
	appsRoot := writeTestRoboSatsApp(t)
	runner := &composeRecordingRunner{runningServices: "tor\nrobosats\nproxy\n"}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, PrivilegedAppsRoot: t.TempDir()}
	inspection, err := manager.Inspect(context.Background(), appmanifest.RoboSatsID)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Status != "running" || inspection.CPUPercentRaw != 0 || len(runner.commands) != 2 {
		t.Fatalf("inspection=%#v commands=%#v", inspection, runner.commands)
	}
	if !hasArgsSuffix(runner.commands[1].args, "ps", "--services", "--filter", "status=running") {
		t.Fatalf("unexpected status command: %#v", runner.commands[1])
	}
}

func TestComposeAppRoboSatsRejectsTamperedAssetsBeforeCommand(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, appsRoot string)
	}{
		{name: "compose", mutate: func(t *testing.T, appsRoot string) {
			path := filepath.Join(appsRoot, appmanifest.RoboSatsID, appmanifest.RoboSatsComposeFile)
			mustWriteTestFile(t, path, []byte("services:\n  proxy:\n    privileged: true\n"), 0600)
		}},
		{name: "caddyfile", mutate: func(t *testing.T, appsRoot string) {
			path := filepath.Join(appsRoot, appmanifest.RoboSatsID, appmanifest.RoboSatsCaddyfileFile)
			mustWriteTestFile(t, path, []byte(":12596 { reverse_proxy attacker:80 }\n"), 0600)
		}},
		{name: "mismatched TLS key", mutate: func(t *testing.T, appsRoot string) {
			_, privateKey := testTLSKeyPair(t)
			path := filepath.Join(appsRoot, appmanifest.RoboSatsID, appmanifest.RoboSatsTLSDir, "server.key")
			mustWriteTestFile(t, path, privateKey, 0600)
		}},
		{name: "symlinked Caddyfile", mutate: func(t *testing.T, appsRoot string) {
			path := filepath.Join(appsRoot, appmanifest.RoboSatsID, appmanifest.RoboSatsCaddyfileFile)
			target := filepath.Join(appsRoot, "Caddyfile-copy")
			mustWriteTestFile(t, target, []byte(appmanifest.RoboSatsCaddyfile()), 0600)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			appsRoot := writeTestRoboSatsApp(t)
			test.mutate(t, appsRoot)
			runner := &composeRecordingRunner{}
			manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, PrivilegedAppsRoot: t.TempDir()}
			if err := manager.Lifecycle(context.Background(), appmanifest.RoboSatsID, AppLifecycleStart, true); err == nil {
				t.Fatal("expected validation error")
			}
			if len(runner.commands) != 0 {
				t.Fatalf("rejected assets executed commands: %#v", runner.commands)
			}
		})
	}
}

func TestComposeAppRoboSatsRejectsSymlinkedPrivilegedRoot(t *testing.T) {
	appsRoot := writeTestRoboSatsApp(t)
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	privilegedAppsRoot := filepath.Join(parent, "privileged-apps")
	if err := os.Symlink(target, privilegedAppsRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, PrivilegedAppsRoot: privilegedAppsRoot}
	if err := manager.Lifecycle(context.Background(), appmanifest.RoboSatsID, AppLifecycleStart, false); err == nil {
		t.Fatal("expected symlinked privileged root to fail")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("symlinked privileged root executed commands: %#v", runner.commands)
	}
}

func TestWriteAtomicRegularFileReplacesExistingContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset")
	if err := writeAtomicRegularFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomicRegularFile(path, []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "second" {
		t.Fatalf("atomic replacement wrote %q", raw)
	}
}

func TestComposeAppLifecycleSupportsFixedStandaloneBinary(t *testing.T) {
	appsRoot, _ := writeTestCPUMinerApp(t)
	runner := &composeRecordingRunner{standalone: true}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, TempRoot: t.TempDir()}
	if err := manager.Lifecycle(context.Background(), "cpuminer", AppLifecycleStop, false); err != nil {
		t.Fatal(err)
	}
	wantTail := []string{"stop", "--timeout", "2"}
	if len(runner.commands) != 3 {
		t.Fatalf("unexpected standalone command sequence: %#v", runner.commands)
	}
	gotArgs := runner.commands[2].args
	if runner.commands[2].path != dockerComposePath || len(gotArgs) < len(wantTail) || !reflect.DeepEqual(gotArgs[len(gotArgs)-len(wantTail):], wantTail) {
		t.Fatalf("unexpected standalone command sequence: %#v", runner.commands)
	}
}

func TestComposeAppLifecycleDryRunValidatesWithoutCommand(t *testing.T) {
	appsRoot, _ := writeTestCPUMinerApp(t)
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot}
	if err := manager.Lifecycle(context.Background(), "cpuminer", AppLifecycleStart, true); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("dry run executed commands: %#v", runner.commands)
	}
}

func TestComposeAppLifecycleRejectsUntrustedInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, root string)
		appID  string
		action AppLifecycleAction
	}{
		{name: "unknown app", appID: "mempool", action: AppLifecycleStart},
		{name: "unknown action", appID: "cpuminer", action: "exec"},
		{name: "modified compose", appID: "cpuminer", action: AppLifecycleStart, mutate: func(t *testing.T, root string) {
			t.Helper()
			path := filepath.Join(root, "cpuminer", appmanifest.CPUMinerComposeFile)
			if err := os.WriteFile(path, []byte(appmanifest.CPUMinerCompose()+"    privileged: true\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "modified environment", appID: "cpuminer", action: AppLifecycleStart, mutate: func(t *testing.T, root string) {
			t.Helper()
			path := filepath.Join(root, "cpuminer", appmanifest.CPUMinerEnvFile)
			if err := os.WriteFile(path, []byte("CPUMINER_IMAGE=evil/root:latest\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlinked compose", appID: "cpuminer", action: AppLifecycleStart, mutate: func(t *testing.T, root string) {
			t.Helper()
			path := filepath.Join(root, "cpuminer", appmanifest.CPUMinerComposeFile)
			target := filepath.Join(root, "copy.yaml")
			if err := os.WriteFile(target, []byte(appmanifest.CPUMinerCompose()), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			appsRoot, _ := writeTestCPUMinerApp(t)
			if test.mutate != nil {
				test.mutate(t, appsRoot)
			}
			runner := &composeRecordingRunner{}
			manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot}
			if err := manager.Lifecycle(context.Background(), test.appID, test.action, true); err == nil {
				t.Fatal("expected validation error")
			}
			if len(runner.commands) != 0 {
				t.Fatalf("rejected request executed commands: %#v", runner.commands)
			}
		})
	}
}

func TestComposeAppRemoveUsesValidatedSnapshotAndFixedCommand(t *testing.T) {
	appsRoot, validEnv := writeTestCPUMinerApp(t)
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, TempRoot: t.TempDir()}
	if err := manager.Remove(context.Background(), "cpuminer", false); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 || !hasArgsSuffix(runner.commands[1].args, "down", "--remove-orphans", "--timeout", "2") {
		t.Fatalf("unexpected remove commands: %#v", runner.commands)
	}
	if runner.composeSnapshot != appmanifest.CPUMinerCompose() || runner.envSnapshot != validEnv {
		t.Fatal("broker did not remove through the validated manifest snapshot")
	}
	for _, arg := range runner.commands[1].args {
		if strings.Contains(arg, appsRoot) {
			t.Fatalf("manager-writable path reached Docker command: %q", arg)
		}
	}
}

func TestComposeAppRemoveDryRunAndTamperFailBeforeCommand(t *testing.T) {
	appsRoot, _ := writeTestCPUMinerApp(t)
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot}
	if err := manager.Remove(context.Background(), "cpuminer", true); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("dry run executed commands: %#v", runner.commands)
	}
	path := filepath.Join(appsRoot, appmanifest.CPUMinerID, appmanifest.CPUMinerEnvFile)
	if err := os.WriteFile(path, []byte("CPUMINER_IMAGE=evil/root:latest\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(context.Background(), "cpuminer", false); err == nil {
		t.Fatal("expected tampered environment to fail")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("tampered remove executed commands: %#v", runner.commands)
	}
}

func TestComposeAppRoboSatsRemoveUsesPersistentSnapshotAndPreservesVolumes(t *testing.T) {
	appsRoot := writeTestRoboSatsApp(t)
	privilegedAppsRoot := t.TempDir()
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, PrivilegedAppsRoot: privilegedAppsRoot}
	if err := manager.Remove(context.Background(), appmanifest.RoboSatsID, false); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 || !hasArgsSuffix(runner.commands[1].args, "down", "--remove-orphans", "--timeout", "2") {
		t.Fatalf("unexpected remove commands: %#v", runner.commands)
	}
	for _, arg := range runner.commands[1].args {
		if arg == "--volumes" {
			t.Fatalf("RoboSats remove unexpectedly deletes named volumes: %#v", runner.commands[1])
		}
		if strings.Contains(arg, appsRoot) {
			t.Fatalf("manager-writable path reached Docker command: %q", arg)
		}
	}
	if !strings.Contains(runner.composeSnapshot, privilegedAppsRoot) {
		t.Fatalf("remove did not use the privileged snapshot:\n%s", runner.composeSnapshot)
	}
	if _, err := os.Stat(filepath.Join(privilegedAppsRoot, appmanifest.RoboSatsID)); !os.IsNotExist(err) {
		t.Fatalf("privileged execution snapshot still exists or stat failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(appsRoot, appmanifest.RoboSatsID, appmanifest.RoboSatsComposeFile)); err != nil {
		t.Fatalf("broker removed manager-owned files: %v", err)
	}
}

func TestComposeAppRoboSatsRemoveFailurePreservesSnapshotForRetry(t *testing.T) {
	appsRoot := writeTestRoboSatsApp(t)
	privilegedAppsRoot := t.TempDir()
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if hasArgsSuffix(args, "down", "--remove-orphans", "--timeout", "2") {
			return "sensitive compose failure", errors.New("failed"), true
		}
		return "", nil, false
	}}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, PrivilegedAppsRoot: privilegedAppsRoot}
	if err := manager.Remove(context.Background(), appmanifest.RoboSatsID, false); err == nil {
		t.Fatal("expected remove failure")
	}
	if _, err := os.Stat(filepath.Join(privilegedAppsRoot, appmanifest.RoboSatsID, appmanifest.RoboSatsComposeFile)); err != nil {
		t.Fatalf("retry snapshot was not preserved: %v", err)
	}
}

func TestComposeAppRoboSatsRemoveRejectsTamperedManifestBeforeCommand(t *testing.T) {
	appsRoot := writeTestRoboSatsApp(t)
	path := filepath.Join(appsRoot, appmanifest.RoboSatsID, appmanifest.RoboSatsComposeFile)
	mustWriteTestFile(t, path, []byte("services:\n  attacker:\n    privileged: true\n"), 0600)
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, PrivilegedAppsRoot: t.TempDir()}
	if err := manager.Remove(context.Background(), appmanifest.RoboSatsID, false); err == nil {
		t.Fatal("expected tampered manifest to fail")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("tampered remove executed commands: %#v", runner.commands)
	}
}

func TestComposeAppInspectStoppedUsesValidatedSnapshot(t *testing.T) {
	appsRoot, validEnv := writeTestCPUMinerApp(t)
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, TempRoot: t.TempDir()}
	inspection, err := manager.Inspect(context.Background(), "cpuminer")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Status != "stopped" || inspection.CPUPercentRaw != 0 || len(runner.commands) != 2 {
		t.Fatalf("inspection=%#v commands=%#v", inspection, runner.commands)
	}
	if !hasArgsSuffix(runner.commands[1].args, "ps", "--services", "--filter", "status=running") {
		t.Fatalf("unexpected status command: %#v", runner.commands[1])
	}
	if runner.composeSnapshot != appmanifest.CPUMinerCompose() || runner.envSnapshot != validEnv {
		t.Fatal("broker did not inspect the validated manifest snapshot")
	}
	for _, arg := range runner.commands[1].args {
		if strings.Contains(arg, appsRoot) {
			t.Fatalf("manager-writable path reached Docker command: %q", arg)
		}
	}
}

func TestComposeAppInspectRunningReturnsDockerCPU(t *testing.T) {
	appsRoot, _ := writeTestCPUMinerApp(t)
	containerID := strings.Repeat("a", 64)
	runner := &composeRecordingRunner{
		runningServices: "cpuminer\n",
		containerID:     containerID + "\n",
		cpuPercent:      "125,75%\n",
	}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, TempRoot: t.TempDir()}
	inspection, err := manager.Inspect(context.Background(), "cpuminer")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Status != "running" || inspection.CPUPercentRaw != 125.75 || len(runner.commands) != 4 {
		t.Fatalf("inspection=%#v commands=%#v", inspection, runner.commands)
	}
	wantStats := recordedCommand{path: dockerPath, args: []string{"stats", "--no-stream", "--format", "{{.CPUPerc}}", containerID}}
	if !reflect.DeepEqual(runner.commands[3], wantStats) {
		t.Fatalf("stats command=%#v want=%#v", runner.commands[3], wantStats)
	}
}

func TestComposeAppInspectDegradesStatsFailureToZero(t *testing.T) {
	appsRoot, _ := writeTestCPUMinerApp(t)
	runner := &composeRecordingRunner{
		runningServices: "cpuminer\n",
		containerID:     strings.Repeat("b", 64),
		statsErr:        errors.New("stats unavailable"),
	}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, TempRoot: t.TempDir()}
	inspection, err := manager.Inspect(context.Background(), "cpuminer")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Status != "running" || inspection.CPUPercentRaw != 0 {
		t.Fatalf("unexpected inspection: %#v", inspection)
	}
}

func TestComposeAppInspectRejectsInvalidContainerIDBeforeStats(t *testing.T) {
	appsRoot, _ := writeTestCPUMinerApp(t)
	runner := &composeRecordingRunner{runningServices: "cpuminer\n", containerID: "abc;reboot\n"}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, TempRoot: t.TempDir()}
	if _, err := manager.Inspect(context.Background(), "cpuminer"); err == nil {
		t.Fatal("expected invalid container ID to fail")
	}
	if len(runner.commands) != 3 {
		t.Fatalf("invalid ID reached stats command: %#v", runner.commands)
	}
}

func TestComposeAppInspectRejectsTamperedManifestBeforeCommand(t *testing.T) {
	appsRoot, _ := writeTestCPUMinerApp(t)
	path := filepath.Join(appsRoot, appmanifest.CPUMinerID, appmanifest.CPUMinerComposeFile)
	if err := os.WriteFile(path, []byte(appmanifest.CPUMinerCompose()+"    privileged: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, TempRoot: t.TempDir()}
	if _, err := manager.Inspect(context.Background(), "cpuminer"); err == nil {
		t.Fatal("expected tampered manifest to fail")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("rejected manifest executed commands: %#v", runner.commands)
	}
}

func TestParseDockerContainerID(t *testing.T) {
	valid := strings.Repeat("c", 64)
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: valid + "\n", want: valid},
		{input: strings.Repeat("d", 12), want: strings.Repeat("d", 12)},
		{input: "ABCDEF123456"},
		{input: "abc;reboot"},
		{input: valid + "\n" + valid},
	} {
		if got := parseDockerContainerID(test.input); got != test.want {
			t.Fatalf("parseDockerContainerID(%q)=%q want=%q", test.input, got, test.want)
		}
	}
}

func TestParseDockerCPUPercent(t *testing.T) {
	for _, test := range []struct {
		input string
		want  float64
	}{
		{input: "0.00%", want: 0},
		{input: "125.75%\n", want: 125.75},
		{input: "7,5%", want: 7.5},
		{input: "NaN%", want: 0},
		{input: "1%; reboot", want: 0},
	} {
		if got := parseDockerCPUPercent(test.input); got != test.want {
			t.Fatalf("parseDockerCPUPercent(%q)=%v want=%v", test.input, got, test.want)
		}
	}
}

func writeTestCPUMinerApp(t *testing.T) (string, string) {
	t.Helper()
	appsRoot := t.TempDir()
	appRoot := filepath.Join(appsRoot, appmanifest.CPUMinerID)
	if err := os.MkdirAll(appRoot, 0750); err != nil {
		t.Fatal(err)
	}
	validEnv := "CPUMINER_IMAGE=jvx1971/cpu-lottery-miner:v1\nPOOL_MODE=brln\nSTRATUM_HOST=btcpool.br-ln.com\nSTRATUM_PORT=3332\nMINING_ADDRESS=bc1qexampleaddress000000000000000000000000\nWORKER_NAME=cpu-lottery\nTHREADS=1\n"
	if err := os.WriteFile(filepath.Join(appRoot, appmanifest.CPUMinerComposeFile), []byte(appmanifest.CPUMinerCompose()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appRoot, appmanifest.CPUMinerEnvFile), []byte(validEnv), 0600); err != nil {
		t.Fatal(err)
	}
	return appsRoot, validEnv
}

func writeTestRoboSatsApp(t *testing.T) string {
	t.Helper()
	appsRoot := t.TempDir()
	appRoot := filepath.Join(appsRoot, appmanifest.RoboSatsID)
	tlsDir := filepath.Join(appRoot, appmanifest.RoboSatsTLSDir)
	if err := os.MkdirAll(tlsDir, 0750); err != nil {
		t.Fatal(err)
	}
	caddyfilePath := filepath.Join(appRoot, appmanifest.RoboSatsCaddyfileFile)
	mustWriteTestFile(t, filepath.Join(appRoot, appmanifest.RoboSatsComposeFile), []byte(appmanifest.RoboSatsCompose(caddyfilePath, tlsDir)), 0600)
	mustWriteTestFile(t, caddyfilePath, []byte(appmanifest.RoboSatsCaddyfile()), 0600)
	certificate, privateKey := testTLSKeyPair(t)
	mustWriteTestFile(t, filepath.Join(tlsDir, "server.crt"), certificate, 0600)
	mustWriteTestFile(t, filepath.Join(tlsDir, "server.key"), privateKey, 0600)
	return appsRoot
}

func testTLSKeyPair(t *testing.T) ([]byte, []byte) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-robosats"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	key := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	return certificate, key
}

func mustWriteTestFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}
