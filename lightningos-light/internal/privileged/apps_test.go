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
	"runtime"
	"strconv"
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

func TestComposeAppPrepareLNbitsUsesPinnedOfficialDigest(t *testing.T) {
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
	state, err := manager.PrepareImage(context.Background(), appmanifest.LNbitsID, appmanifest.LNbitsImageApp, false)
	if err != nil || state.Status != "preparing" || len(runner.commands) != 3 {
		t.Fatalf("state=%#v err=%v commands=%#v", state, err, runner.commands)
	}
	want := recordedCommand{path: systemdRunPath, args: []string{
		"--quiet", "--collect", "--unit=lightningos-lnbits-image-app",
		"--property=Type=exec", "--property=RuntimeMaxSec=10min",
		dockerPath, "pull", appmanifest.LNbitsImage,
	}}
	if !reflect.DeepEqual(runner.commands[2], want) {
		t.Fatalf("pull command=%#v want=%#v", runner.commands[2], want)
	}
}

func TestComposeAppPrepareBTCPayReleaseAlwaysSchedulesRefresh(t *testing.T) {
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner}
	state, err := manager.PrepareImage(context.Background(), appmanifest.BTCPayID, appmanifest.BTCPayImageServer, false)
	if err != nil || state.Status != "preparing" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	if len(runner.commands) != 3 {
		t.Fatalf("cached latest image did not schedule refresh: %#v", runner.commands)
	}
	want := recordedCommand{path: systemdRunPath, args: []string{
		"--quiet", "--collect", "--unit=lightningos-btcpay-image-server",
		"--property=Type=exec", "--property=RuntimeMaxSec=10min",
		dockerPath, "pull", appmanifest.BTCPayServerImage,
	}}
	if !reflect.DeepEqual(runner.commands[2], want) {
		t.Fatalf("pull command=%#v want=%#v", runner.commands[2], want)
	}
}

func TestComposeAppBTCPayReleaseStatusWaitsForRefreshUnit(t *testing.T) {
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == systemctlPath && len(args) > 0 && args[0] == "show" {
			return "LoadState=loaded\nActiveState=active\nSubState=running\nResult=success\n", nil, true
		}
		return "", nil, false
	}}
	manager := &ComposeAppManager{Runner: runner}
	state, err := manager.ImageStatus(context.Background(), appmanifest.BTCPayID, appmanifest.BTCPayImageServer)
	if err != nil || state.Status != "preparing" || len(runner.commands) != 1 {
		t.Fatalf("state=%#v err=%v commands=%#v", state, err, runner.commands)
	}
}

func TestComposeAppPrepareBTCPayPinnedDependencyUsesCache(t *testing.T) {
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner}
	state, err := manager.PrepareImage(context.Background(), appmanifest.BTCPayID, appmanifest.BTCPayImageNbxplorer, false)
	if err != nil || state.Status != "ready" || len(runner.commands) != 1 {
		t.Fatalf("state=%#v err=%v commands=%#v", state, err, runner.commands)
	}
	if !reflect.DeepEqual(runner.commands[0], recordedCommand{path: dockerPath, args: []string{"image", "inspect", appmanifest.BTCPayNbxplorerImage}}) {
		t.Fatalf("unexpected pinned dependency command: %#v", runner.commands[0])
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

func TestComposeAppTapdProbeRequiresBothExactBinaryVersions(t *testing.T) {
	image := appmanifest.TapdImage
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path != dockerPath {
			return "", nil, false
		}
		if reflect.DeepEqual(args, []string{"image", "inspect", image}) {
			return "[]", nil, true
		}
		if len(args) >= 3 && hasArgsSuffix(args, "--entrypoint", "/bin/tapd", image, "--version") {
			return appmanifest.TapdDaemonVersionOutput + "\n", nil, true
		}
		if len(args) >= 3 && hasArgsSuffix(args, "--entrypoint", "/bin/tapcli", image, "--version") {
			return appmanifest.TapdCLIVersionOutput + "\n", nil, true
		}
		return "", errors.New("unexpected command"), true
	}}
	manager := &ComposeAppManager{Runner: runner}
	probe, err := manager.ProbeImage(context.Background(), appmanifest.TapdID, appmanifest.TapdImageApp, false)
	if err != nil || !probe.Runnable {
		t.Fatalf("probe/error=%#v/%v", probe, err)
	}
	if len(runner.commands) != 3 {
		t.Fatalf("commands=%#v", runner.commands)
	}
	for _, command := range runner.commands[1:] {
		for _, expected := range []string{"--pull", "never", "--network", "none", "--read-only", "--user", "65534:65534", "--cap-drop", "ALL", "--security-opt", "no-new-privileges"} {
			if !hasArg(command.args, expected) {
				t.Fatalf("probe command missing %q: %#v", expected, command)
			}
		}
	}

	runner.hook = func(path string, args []string) (string, error, bool) {
		if reflect.DeepEqual(args, []string{"image", "inspect", image}) {
			return "[]", nil, true
		}
		if hasArgsSuffix(args, "--entrypoint", "/bin/tapd", image, "--version") {
			return appmanifest.TapdDaemonVersionOutput, nil, true
		}
		if hasArgsSuffix(args, "--entrypoint", "/bin/tapcli", image, "--version") {
			return "tapcli version unexpected", nil, true
		}
		return "", errors.New("unexpected command"), true
	}
	probe, err = manager.ProbeImage(context.Background(), appmanifest.TapdID, appmanifest.TapdImageApp, false)
	if err != nil || probe.Runnable {
		t.Fatalf("mismatched probe/error=%#v/%v", probe, err)
	}
}

func TestComposeAppPublicPoolProbesRequireExactHardenedRuntime(t *testing.T) {
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path != dockerPath {
			return "", nil, false
		}
		if len(args) == 3 && args[0] == "image" && args[1] == "inspect" {
			return "[]", nil, true
		}
		if hasArgsSuffix(args, "--entrypoint", "/usr/local/bin/node", appmanifest.PublicPoolBackendImage, "--version") {
			return appmanifest.PublicPoolBackendVersionOutput, nil, true
		}
		if hasArgsSuffix(args, "--entrypoint", "/bin/sh", appmanifest.PublicPoolUIImage, "-c", "cp /usr/bin/caddy /run/lightningos-bin/caddy && exec /run/lightningos-bin/caddy version") {
			return appmanifest.PublicPoolUIVersionOutput, nil, true
		}
		return "", errors.New("unexpected command"), true
	}}
	manager := &ComposeAppManager{Runner: runner}
	for _, variant := range appmanifest.PublicPoolImageVariants() {
		probe, err := manager.ProbeImage(context.Background(), appmanifest.PublicPoolID, variant, false)
		if err != nil || !probe.Runnable {
			t.Fatalf("probe %s/error=%#v/%v", variant, probe, err)
		}
		command := runner.commands[len(runner.commands)-1]
		for _, expected := range []string{"--pull", "never", "--network", "none", "--read-only", "--user", "65532:65532", "--cap-drop", "ALL", "--security-opt", "no-new-privileges"} {
			if !hasArg(command.args, expected) {
				t.Fatalf("probe command missing %q: %#v", expected, command)
			}
		}
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

func hasArg(args []string, expected string) bool {
	for _, arg := range args {
		if arg == expected {
			return true
		}
	}
	return false
}

func hasArgsPrefix(args []string, prefix ...string) bool {
	return len(args) >= len(prefix) && reflect.DeepEqual(args[:len(prefix)], prefix)
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

func TestComposeAppBTCPayCreatesSecretSafePersistentSnapshot(t *testing.T) {
	fixture := writeTestBTCPayApp(t, false, false)
	snapshot, cleanup, err := fixture.manager.prepareBTCPaySnapshot(false)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	wantRoot := filepath.Join(fixture.privilegedRoot, appmanifest.BTCPayID)
	if snapshot.root != wantRoot || snapshot.composePath != filepath.Join(wantRoot, appmanifest.BTCPayComposeFile) || snapshot.envPath != filepath.Join(wantRoot, appmanifest.BTCPayEnvFile) {
		t.Fatalf("unexpected BTCPay snapshot: %#v", snapshot)
	}
	composeRaw, err := os.ReadFile(snapshot.composePath)
	if err != nil {
		t.Fatal(err)
	}
	compose := string(composeRaw)
	for _, required := range []string{
		filepath.Join(wantRoot, appmanifest.BTCPayDBInitFile),
		filepath.Join(wantRoot, appmanifest.BTCPayLNDDir),
		"macaroonfilepath=/etc/lnd/" + appmanifest.BTCPaySnapshotAuthFile,
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("execution compose missing %q:\n%s", required, compose)
		}
	}
	for _, forbidden := range []string{"admin.macaroon", ".macaroon", fixture.appRoot} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("execution compose contains forbidden manager asset %q", forbidden)
		}
	}
	authPath := filepath.Join(wantRoot, appmanifest.BTCPayLNDDir, appmanifest.BTCPaySnapshotAuthFile)
	authRaw, err := os.ReadFile(authPath)
	if err != nil || string(authRaw) != testBTCPayDedicatedMacaroon {
		t.Fatalf("dedicated credential was not snapshotted: %q/%v", authRaw, err)
	}
	if _, err := os.Lstat(filepath.Join(wantRoot, appmanifest.BTCPayLNDDir, appmanifest.BTCPayMacaroonFile)); !os.IsNotExist(err) {
		t.Fatalf("snapshot exposed a .macaroon file: %v", err)
	}
	for _, path := range []string{
		snapshot.composePath,
		snapshot.envPath,
		filepath.Join(wantRoot, appmanifest.BTCPayDBInitFile),
		filepath.Join(wantRoot, appmanifest.BTCPayLNDDir, appmanifest.BTCPayTLSCertFile),
		authPath,
	} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || (runtime.GOOS == "linux" && info.Mode().Perm() != 0600) {
			t.Fatalf("snapshot asset is not a 0600 regular file: %s: %v/%v", path, info, err)
		}
		if runtime.GOOS == "linux" && os.Geteuid() == 0 && !privilegedPathOwnedByRoot(info) {
			t.Fatalf("snapshot asset is not owned by root: %s", path)
		}
	}
	for _, path := range []string{wantRoot, filepath.Join(wantRoot, appmanifest.BTCPayLNDDir)} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || (runtime.GOOS == "linux" && info.Mode().Perm() != 0700) {
			t.Fatalf("snapshot directory is not a 0700 directory: %s: %v/%v", path, info, err)
		}
		if runtime.GOOS == "linux" && os.Geteuid() == 0 && !privilegedPathOwnedByRoot(info) {
			t.Fatalf("snapshot directory is not owned by root: %s", path)
		}
	}
	if _, err := os.Stat(wantRoot); err != nil {
		t.Fatalf("persistent snapshot was unexpectedly cleaned: %v", err)
	}
}

func TestComposeAppBTCPayDryRunValidatesWithoutSnapshot(t *testing.T) {
	fixture := writeTestBTCPayApp(t, false, false)
	if _, _, err := fixture.manager.prepareBTCPaySnapshot(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(fixture.privilegedRoot, appmanifest.BTCPayID)); !os.IsNotExist(err) {
		t.Fatalf("dry-run created a privileged snapshot: %v", err)
	}
}

func TestComposeAppBTCPayLifecycleRunsOnlyFromPrivilegedSnapshot(t *testing.T) {
	fixture := writeTestBTCPayApp(t, false, false)
	runner := fixture.manager.Runner.(*composeRecordingRunner)
	runner.hook = func(path string, args []string) (string, error, bool) {
		if path == dockerPath && hasArgsSuffix(args, "exec", "-T", "btcpay-db", "psql", "-U", "btcpay", "-d", "btcpayserver", "-tAc", "SELECT 1 FROM pg_database WHERE datname = 'nbxplorer'") {
			return "1\n", nil, true
		}
		return "", nil, false
	}

	if err := fixture.manager.Lifecycle(context.Background(), appmanifest.BTCPayID, AppLifecycleStart, false); err != nil {
		t.Fatal(err)
	}
	wantSnapshotRoot := filepath.Join(fixture.privilegedRoot, appmanifest.BTCPayID)
	databaseStart := -1
	postgresReady := -1
	fullStart := -1
	for index, command := range runner.commands {
		joined := strings.Join(command.args, " ")
		if strings.Contains(joined, fixture.appRoot) {
			t.Fatalf("manager-owned BTCPay root reached Docker: %#v", command)
		}
		if hasArgsSuffix(command.args, "up", "-d", "btcpay-db") {
			databaseStart = index
		}
		if command.path == dockerPath && hasArgsSuffix(command.args, "exec", "-T", "btcpay-db", "pg_isready", "-U", "btcpay", "-d", "btcpayserver") {
			postgresReady = index
		}
		if hasArgsSuffix(command.args, "up", "-d") {
			fullStart = index
		}
		if command.path == dockerPath && len(command.args) >= 2 && command.args[0] == "compose" && command.args[1] != "version" {
			if !strings.Contains(joined, "--project-directory "+wantSnapshotRoot) || !strings.Contains(joined, "-f "+filepath.Join(wantSnapshotRoot, appmanifest.BTCPayComposeFile)) {
				t.Fatalf("Compose did not use the broker snapshot: %#v", command)
			}
		}
	}
	if databaseStart < 0 || postgresReady <= databaseStart || fullStart <= postgresReady {
		t.Fatalf("invalid BTCPay start/database order: %#v", runner.commands)
	}
	if strings.Contains(runner.composeSnapshot, ".macaroon") || !strings.Contains(runner.composeSnapshot, "macaroonfilepath=/etc/lnd/"+appmanifest.BTCPaySnapshotAuthFile) {
		t.Fatalf("execution snapshot exposed the wrong LND credential: %s", runner.composeSnapshot)
	}
}

func TestComposeAppBTCPayLifecycleChecksOnlyCatalogImages(t *testing.T) {
	fixture := writeTestBTCPayApp(t, false, true)
	runner := fixture.manager.Runner.(*composeRecordingRunner)
	runner.hook = func(path string, args []string) (string, error, bool) {
		if path == dockerPath && hasArgsSuffix(args, "exec", "-T", "btcpay-db", "psql", "-U", "btcpay", "-d", "btcpayserver", "-tAc", "SELECT 1 FROM pg_database WHERE datname = 'nbxplorer'") {
			return "1\n", nil, true
		}
		return "", nil, false
	}
	if err := fixture.manager.Lifecycle(context.Background(), appmanifest.BTCPayID, AppLifecycleStart, false); err != nil {
		t.Fatal(err)
	}
	var inspected []string
	for _, command := range runner.commands {
		if command.path == dockerPath && len(command.args) == 3 && command.args[0] == "image" && command.args[1] == "inspect" {
			inspected = append(inspected, command.args[2])
		}
	}
	if want := appmanifest.BTCPayImages(true); !reflect.DeepEqual(inspected, want) {
		t.Fatalf("inspected images=%#v want=%#v", inspected, want)
	}
}

func TestComposeAppBTCPayPostgresRepairIsClosedAndIdempotent(t *testing.T) {
	tests := []struct {
		name         string
		queryOutputs []string
		queryErrors  []error
		wantCommands int
		wantCreate   bool
	}{
		{name: "existing database", queryOutputs: []string{"1\n"}, wantCommands: 2},
		{name: "missing database", queryOutputs: []string{""}, wantCommands: 3, wantCreate: true},
		{name: "initialization restart", queryOutputs: []string{"shutting down", ""}, queryErrors: []error{errors.New("temporary")}, wantCommands: 5, wantCreate: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &composeRecordingRunner{}
			queryCall := 0
			composeArgs := []string{"compose", "--project-name", appmanifest.BTCPayID}
			runner.hook = func(path string, args []string) (string, error, bool) {
				if path == dockerPath && len(args) >= len(composeArgs)+4 && hasArgsPrefix(args, composeArgs...) && args[len(composeArgs)] == "exec" && args[len(composeArgs)+1] == "-T" && args[len(composeArgs)+2] == "btcpay-db" {
					switch args[len(composeArgs)+3] {
					case "pg_isready", "createdb":
						return "", nil, true
					case "psql":
						output := test.queryOutputs[queryCall]
						var err error
						if queryCall < len(test.queryErrors) {
							err = test.queryErrors[queryCall]
						}
						queryCall++
						return output, err, true
					}
				}
				return "", errors.New("unexpected command"), true
			}
			manager := &ComposeAppManager{Runner: runner}
			if err := manager.ensureBTCPayNbxplorerDatabaseWithPolicy(context.Background(), dockerPath, composeArgs, 3, 0); err != nil {
				t.Fatal(err)
			}
			if len(runner.commands) != test.wantCommands {
				t.Fatalf("commands=%#v", runner.commands)
			}
			last := runner.commands[len(runner.commands)-1]
			created := last.path == dockerPath && hasArgsSuffix(last.args, "exec", "-T", "btcpay-db", "createdb", "-U", "btcpay", "--owner=btcpay", "--template=template0", "--encoding=UTF8", "--lc-collate=C", "--lc-ctype=C", "nbxplorer")
			if created != test.wantCreate {
				t.Fatalf("created=%v commands=%#v", created, runner.commands)
			}
			if created {
				joined := strings.Join(last.args, " ")
				for _, required := range []string{"--owner=btcpay", "--template=template0", "--encoding=UTF8", "--lc-collate=C", "--lc-ctype=C", "nbxplorer"} {
					if !strings.Contains(joined, required) {
						t.Fatalf("createdb command missing %q: %s", required, joined)
					}
				}
			}
		})
	}
}

func TestComposeAppBTCPayPostgresFailureDoesNotLeakOutput(t *testing.T) {
	composeArgs := []string{"compose", "--project-name", appmanifest.BTCPayID}
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == dockerPath && hasArgsPrefix(args, composeArgs...) && hasArgsSuffix(args, "exec", "-T", "btcpay-db", "pg_isready", "-U", "btcpay", "-d", "btcpayserver") {
			return "rpc-password=do-not-log", errors.New("secret stderr"), true
		}
		return "", errors.New("unexpected command"), true
	}}
	manager := &ComposeAppManager{Runner: runner}
	err := manager.ensureBTCPayNbxplorerDatabaseWithPolicy(context.Background(), dockerPath, composeArgs, 2, 0)
	if err == nil || strings.Contains(err.Error(), "do-not-log") || strings.Contains(err.Error(), "secret stderr") {
		t.Fatalf("unsafe postgres error: %v", err)
	}
}

func TestComposeAppBTCPayPostgresRepairRejectsUnexpectedComposeCommand(t *testing.T) {
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner}
	err := manager.ensureBTCPayNbxplorerDatabaseWithPolicy(
		context.Background(),
		"/tmp/untrusted-compose",
		[]string{"--project-name", appmanifest.BTCPayID},
		1,
		0,
	)
	if err == nil || err.Error() != "invalid BTCPay compose command" {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("unexpected command execution: %#v", runner.commands)
	}
}

func TestComposeAppBTCPayStopUsesSnapshotWithoutDatabaseCommands(t *testing.T) {
	fixture := writeTestBTCPayApp(t, false, false)
	runner := fixture.manager.Runner.(*composeRecordingRunner)
	if err := fixture.manager.Lifecycle(context.Background(), appmanifest.BTCPayID, AppLifecycleStop, false); err != nil {
		t.Fatal(err)
	}
	for _, command := range runner.commands {
		if command.path == dockerPath && len(command.args) > 0 && command.args[0] == "exec" {
			t.Fatalf("stop invoked a database command: %#v", command)
		}
	}
	last := runner.commands[len(runner.commands)-1]
	if !hasArgsSuffix(last.args, "stop", "--timeout", strconv.Itoa(appmanifest.BTCPayStopTimeout)) || strings.Contains(strings.Join(last.args, " "), fixture.appRoot) {
		t.Fatalf("unsafe BTCPay stop command: %#v", last)
	}
}

func TestComposeAppBTCPayRemoveUsesAndDeletesOnlySnapshot(t *testing.T) {
	fixture := writeTestBTCPayApp(t, false, false)
	runner := fixture.manager.Runner.(*composeRecordingRunner)
	if err := fixture.manager.Remove(context.Background(), appmanifest.BTCPayID, false); err != nil {
		t.Fatal(err)
	}
	last := runner.commands[len(runner.commands)-1]
	if !hasArgsSuffix(last.args, "down", "--remove-orphans", "--timeout", strconv.Itoa(appmanifest.BTCPayStopTimeout)) || strings.Contains(strings.Join(last.args, " "), fixture.appRoot) {
		t.Fatalf("unsafe BTCPay remove command: %#v", last)
	}
	if _, err := os.Stat(filepath.Join(fixture.privilegedRoot, appmanifest.BTCPayID)); !os.IsNotExist(err) {
		t.Fatalf("privileged snapshot was not removed: %v", err)
	}
	if _, err := os.Stat(fixture.appRoot); err != nil {
		t.Fatalf("broker removed manager-owned app data: %v", err)
	}
}

func TestComposeAppBTCPayRejectsUntrustedSecretAssets(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, fixture *testBTCPayFixture)
	}{
		{name: "tampered compose", mutate: func(t *testing.T, fixture *testBTCPayFixture) {
			mustWriteTestFile(t, fixture.composePath, []byte(appmanifest.BTCPayCompose(fixture.composePaths, false, false)+"# tampered\n"), 0600)
		}},
		{name: "unknown env key", mutate: func(t *testing.T, fixture *testBTCPayFixture) {
			mustWriteTestFile(t, fixture.envPath, []byte(fixture.envRaw+"ATTACKER_IMAGE=evil/root:latest\n"), 0600)
		}},
		{name: "duplicate env key", mutate: func(t *testing.T, fixture *testBTCPayFixture) {
			mustWriteTestFile(t, fixture.envPath, []byte(fixture.envRaw+"BTCPAY_DB_PASSWORD="+strings.Repeat("b", 32)+"\n"), 0600)
		}},
		{name: "compose interpolation in secret", mutate: func(t *testing.T, fixture *testBTCPayFixture) {
			raw := strings.Replace(fixture.envRaw, "NBXPLORER_BTCRPCPASSWORD=rpc-pass", "NBXPLORER_BTCRPCPASSWORD=${ADMIN}", 1)
			mustWriteTestFile(t, fixture.envPath, []byte(raw), 0600)
		}},
		{name: "admin macaroon content", mutate: func(t *testing.T, fixture *testBTCPayFixture) {
			adminRaw, err := os.ReadFile(fixture.adminMacaroonPath)
			if err != nil {
				t.Fatal(err)
			}
			mustWriteTestFile(t, fixture.macaroonPath, adminRaw, 0600)
		}},
		{name: "admin macaroon hardlink", mutate: func(t *testing.T, fixture *testBTCPayFixture) {
			if err := os.Remove(fixture.macaroonPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(fixture.adminMacaroonPath, fixture.macaroonPath); err != nil {
				t.Skipf("hardlink unavailable: %v", err)
			}
		}},
		{name: "symlinked dedicated macaroon", mutate: func(t *testing.T, fixture *testBTCPayFixture) {
			if err := os.Remove(fixture.macaroonPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(fixture.adminMacaroonPath, fixture.macaroonPath); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}},
		{name: "mismatched native certificate", mutate: func(t *testing.T, fixture *testBTCPayFixture) {
			certificate, _ := testTLSKeyPair(t)
			mustWriteTestFile(t, fixture.certificatePath, certificate, 0600)
		}},
		{name: "world readable environment", mutate: func(t *testing.T, fixture *testBTCPayFixture) {
			if runtime.GOOS != "linux" {
				t.Skip("POSIX permission enforcement is validated on Linux")
			}
			if err := os.Chmod(fixture.envPath, 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "unexpected admin file in snapshot", mutate: func(t *testing.T, fixture *testBTCPayFixture) {
			lndSnapshot := filepath.Join(fixture.privilegedRoot, appmanifest.BTCPayID, appmanifest.BTCPayLNDDir)
			if err := os.MkdirAll(lndSnapshot, 0700); err != nil {
				t.Fatal(err)
			}
			mustWriteTestFile(t, filepath.Join(lndSnapshot, "admin.macaroon"), []byte("forbidden"), 0600)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := writeTestBTCPayApp(t, false, false)
			test.mutate(t, fixture)
			if _, _, err := fixture.manager.prepareBTCPaySnapshot(false); err == nil {
				t.Fatal("expected BTCPay snapshot validation to fail")
			}
		})
	}
}

func TestValidateBTCPayEnvAcceptsOnlyCoherentWiring(t *testing.T) {
	onion := strings.Repeat("a", 56) + ".onion"
	for _, test := range []struct {
		name     string
		env      string
		wantJoin bool
		wantTor  bool
		wantErr  bool
	}{
		{name: "remote clearnet", env: testBTCPayEnv("http://bitcoin.example.com:8332/", "bitcoin.example.com:8333", "")},
		{name: "remote onion", env: testBTCPayEnv("https://bitcoin.example.com:8332/", onion+":8333", "tor:9050"), wantTor: true},
		{name: "app store bitcoin", env: testBTCPayEnv("http://bitcoind:8332/", "bitcoind:8333", ""), wantJoin: true},
		{name: "native bitcoin", env: testBTCPayEnv("http://"+appmanifest.BitcoinConsumerHostGateway+":18443/", appmanifest.BitcoinConsumerHostGateway+":8333", ""), wantJoin: true},
		{name: "stale socks", env: testBTCPayEnv("http://bitcoin.example.com:8332/", "bitcoin.example.com:8333", "tor:9050"), wantErr: true},
		{name: "onion without socks", env: testBTCPayEnv("http://bitcoin.example.com:8332/", onion+":8333", ""), wantErr: true},
		{name: "mixed local endpoints", env: testBTCPayEnv("http://bitcoind:8332/", "bitcoin.example.com:8333", ""), wantErr: true},
		{name: "URL credentials", env: testBTCPayEnv("http://user:pass@bitcoin.example.com:8332/", "bitcoin.example.com:8333", ""), wantErr: true},
		{name: "query injection", env: testBTCPayEnv("http://bitcoin.example.com:8332/?x=1", "bitcoin.example.com:8333", ""), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			join, tor, err := validateBTCPayEnv([]byte(test.env))
			if (err != nil) != test.wantErr || (!test.wantErr && (join != test.wantJoin || tor != test.wantTor)) {
				t.Fatalf("join/tor/error=%v/%v/%v", join, tor, err)
			}
		})
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

const (
	testBTCPayDedicatedMacaroon = "dedicated-btcpay-macaroon-binary"
	testBTCPayAdminMacaroon     = "native-admin-macaroon-binary"
)

type testBTCPayFixture struct {
	manager           *ComposeAppManager
	appRoot           string
	privilegedRoot    string
	composePath       string
	envPath           string
	envRaw            string
	composePaths      appmanifest.BTCPayComposePaths
	certificatePath   string
	macaroonPath      string
	adminMacaroonPath string
}

func writeTestBTCPayApp(t *testing.T, joinBitcoinNetwork bool, useTorProxy bool) *testBTCPayFixture {
	t.Helper()
	appsRoot := filepath.Join(t.TempDir(), "apps")
	appsDataRoot := filepath.Join(t.TempDir(), "apps-data")
	privilegedRoot := filepath.Join(t.TempDir(), "privileged-apps")
	lndDataRoot := filepath.Join(t.TempDir(), "native-lnd")
	appRoot := filepath.Join(appsRoot, appmanifest.BTCPayID)
	dataRoot := filepath.Join(appsDataRoot, appmanifest.BTCPayID)
	lndDir := filepath.Join(dataRoot, appmanifest.BTCPayLNDDir)
	for _, directory := range []string{
		appRoot,
		filepath.Join(dataRoot, "data"),
		filepath.Join(dataRoot, "nbxplorer"),
		filepath.Join(dataRoot, "pgdata"),
		lndDir,
		filepath.Join(lndDataRoot, "data", "chain", "bitcoin", "mainnet"),
	} {
		if err := os.MkdirAll(directory, 0750); err != nil {
			t.Fatal(err)
		}
	}
	composePaths := appmanifest.BTCPayComposePaths{
		DataDir:    filepath.Join(dataRoot, "data"),
		NbxDir:     filepath.Join(dataRoot, "nbxplorer"),
		PgDir:      filepath.Join(dataRoot, "pgdata"),
		DbInitPath: filepath.Join(appRoot, appmanifest.BTCPayDBInitFile),
		LndDir:     lndDir,
	}
	rpcURL := "http://bitcoin.example.com:8332/"
	nodeEndpoint := "bitcoin.example.com:8333"
	socksEndpoint := ""
	if joinBitcoinNetwork {
		rpcURL = "http://bitcoind:8332/"
		nodeEndpoint = "bitcoind:8333"
	}
	if useTorProxy {
		nodeEndpoint = strings.Repeat("a", 56) + ".onion:8333"
		socksEndpoint = "tor:9050"
	}
	envRaw := testBTCPayEnv(rpcURL, nodeEndpoint, socksEndpoint)
	composePath := filepath.Join(appRoot, appmanifest.BTCPayComposeFile)
	envPath := filepath.Join(appRoot, appmanifest.BTCPayEnvFile)
	certificatePath := filepath.Join(lndDir, appmanifest.BTCPayTLSCertFile)
	macaroonPath := filepath.Join(lndDir, appmanifest.BTCPayMacaroonFile)
	adminMacaroonPath := filepath.Join(lndDataRoot, "data", "chain", "bitcoin", "mainnet", "admin.macaroon")
	mustWriteTestFile(t, composePath, []byte(appmanifest.BTCPayCompose(composePaths, joinBitcoinNetwork, useTorProxy)), 0600)
	mustWriteTestFile(t, envPath, []byte(envRaw), 0600)
	mustWriteTestFile(t, composePaths.DbInitPath, []byte(appmanifest.BTCPayDBInit()), 0600)
	certificate, _ := testTLSKeyPair(t)
	mustWriteTestFile(t, certificatePath, certificate, 0600)
	mustWriteTestFile(t, filepath.Join(lndDataRoot, appmanifest.BTCPayTLSCertFile), certificate, 0600)
	mustWriteTestFile(t, macaroonPath, []byte(testBTCPayDedicatedMacaroon), 0600)
	mustWriteTestFile(t, adminMacaroonPath, []byte(testBTCPayAdminMacaroon), 0600)
	return &testBTCPayFixture{
		manager: &ComposeAppManager{
			Runner:             &composeRecordingRunner{},
			AppsRoot:           appsRoot,
			AppsDataRoot:       appsDataRoot,
			PrivilegedAppsRoot: privilegedRoot,
			LNDDataRoot:        lndDataRoot,
		},
		appRoot:           appRoot,
		privilegedRoot:    privilegedRoot,
		composePath:       composePath,
		envPath:           envPath,
		envRaw:            envRaw,
		composePaths:      composePaths,
		certificatePath:   certificatePath,
		macaroonPath:      macaroonPath,
		adminMacaroonPath: adminMacaroonPath,
	}
}

func testBTCPayEnv(rpcURL string, nodeEndpoint string, socksEndpoint string) string {
	lines := []string{
		"BTCPAY_DB_PASSWORD=" + strings.Repeat("a", 32),
		"NBXPLORER_BTCRPCURL=" + rpcURL,
		"NBXPLORER_BTCRPCUSER=rpc-user",
		"NBXPLORER_BTCRPCPASSWORD=rpc-pass",
		"NBXPLORER_BTCNODEENDPOINT=" + nodeEndpoint,
	}
	if socksEndpoint != "" {
		lines = append(lines, "NBXPLORER_SOCKSENDPOINT="+socksEndpoint)
	}
	return strings.Join(lines, "\n") + "\n"
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
