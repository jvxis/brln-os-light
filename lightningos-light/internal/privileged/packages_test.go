package privileged

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type packageRecordingRunner struct {
	commands []recordedCommand
	hook     func(path string, args []string) (string, error)
}

func (runner *packageRecordingRunner) Run(_ context.Context, path string, args ...string) (string, error) {
	runner.commands = append(runner.commands, recordedCommand{path: path, args: append([]string(nil), args...)})
	if runner.hook != nil {
		return runner.hook(path, args)
	}
	return "", nil
}

func newPackageManager(t *testing.T, runner CommandRunner, release string) *CatalogPackageManager {
	t.Helper()
	path := filepath.Join(t.TempDir(), "os-release")
	if err := os.WriteFile(path, []byte(release), 0o600); err != nil {
		t.Fatal(err)
	}
	return &CatalogPackageManager{Runner: runner, OSReleasePath: path}
}

func TestCatalogPackageEnsureDockerSchedulesOnlyFixedIndexCommand(t *testing.T) {
	runner := &packageRecordingRunner{hook: func(path string, args []string) (string, error) {
		if path == systemctlPath {
			return "LoadState=not-found\nActiveState=inactive\n", errors.New("not found")
		}
		if path == dpkgQueryPath {
			return "", errors.New("missing")
		}
		return "", nil
	}}
	manager := newPackageManager(t, runner, "ID=ubuntu\nVERSION_ID=24.04\n")
	state, err := manager.EnsureFeature(context.Background(), PackageFeatureDockerRuntime, false)
	if err != nil || state.Status != "indexing" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	last := runner.commands[len(runner.commands)-1]
	want := recordedCommand{path: systemdRunPath, args: []string{
		"--quiet", "--no-block", "--collect", "--unit=" + dockerIndexUnit,
		"--property=Type=oneshot", "--property=RemainAfterExit=yes", "--property=RuntimeMaxSec=15min",
		"--setenv=DEBIAN_FRONTEND=noninteractive", flockPath, "--exclusive", packageFeatureLock,
		aptGetPath, "-o", "DPkg::Lock::Timeout=300", "update",
	}}
	if !reflect.DeepEqual(last, want) {
		t.Fatalf("scheduled=%#v want=%#v", last, want)
	}
}

func TestCatalogPackageEnsureDockerSchedulesFixedInstallAfterIndex(t *testing.T) {
	runner := &packageRecordingRunner{hook: func(path string, args []string) (string, error) {
		if path == dpkgQueryPath {
			return "", errors.New("missing")
		}
		if path == systemctlPath && args[len(args)-1] == dockerInstallUnit {
			return "LoadState=not-found\nActiveState=inactive\n", errors.New("not found")
		}
		if path == systemctlPath && args[len(args)-1] == dockerIndexUnit {
			return "LoadState=loaded\nActiveState=active\nSubState=exited\nResult=success\n", nil
		}
		return "", nil
	}}
	manager := newPackageManager(t, runner, "ID=ubuntu\nVERSION_ID=26.04\n")
	state, err := manager.EnsureFeature(context.Background(), PackageFeatureDockerRuntime, false)
	if err != nil || state.Status != "installing" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	last := runner.commands[len(runner.commands)-1]
	wantTail := []string{aptGetPath, "-o", "DPkg::Lock::Timeout=300", "install", "-y", "docker.io", "docker-compose-v2"}
	if last.path != systemdRunPath || !hasArgsSuffix(last.args, wantTail...) {
		t.Fatalf("unexpected install schedule: %#v", last)
	}
}

func TestCatalogPackageFeatureReadyRequiresBothCatalogPackages(t *testing.T) {
	runner := &packageRecordingRunner{hook: func(path string, args []string) (string, error) {
		if path == systemctlPath {
			return "LoadState=not-found\nActiveState=inactive\n", errors.New("not found")
		}
		if path == dpkgQueryPath {
			return "docker.io=installed\ndocker-compose-v2=installed\n", nil
		}
		return "", nil
	}}
	manager := newPackageManager(t, runner, "ID=ubuntu\nVERSION_ID=24.04\n")
	state, err := manager.FeatureStatus(context.Background(), PackageFeatureDockerRuntime)
	if err != nil || state.Status != "ready" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	if len(runner.commands) != 2 || runner.commands[0].path != systemctlPath || runner.commands[1].path != dpkgQueryPath {
		t.Fatalf("unexpected commands: %#v", runner.commands)
	}
}

func TestCatalogPackageEnsureMDNSSchedulesOnlyFixedPackages(t *testing.T) {
	runner := &packageRecordingRunner{hook: func(path string, args []string) (string, error) {
		if path == dpkgQueryPath {
			return "", errors.New("missing")
		}
		if path == systemctlPath && args[len(args)-1] == mdnsInstallUnit {
			return "LoadState=not-found\nActiveState=inactive\n", errors.New("not found")
		}
		if path == systemctlPath && args[len(args)-1] == mdnsIndexUnit {
			return "LoadState=loaded\nActiveState=active\nSubState=exited\nResult=success\n", nil
		}
		return "", nil
	}}
	manager := newPackageManager(t, runner, "ID=ubuntu\nVERSION_ID=24.04\n")
	state, err := manager.EnsureFeature(context.Background(), PackageFeatureMDNS, false)
	if err != nil || state.Status != "installing" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	last := runner.commands[len(runner.commands)-1]
	wantTail := []string{aptGetPath, "-o", "DPkg::Lock::Timeout=300", "install", "-y", "avahi-daemon", "libnss-mdns"}
	if last.path != systemdRunPath || last.args[3] != "--unit="+mdnsInstallUnit || !hasArgsSuffix(last.args, wantTail...) {
		t.Fatalf("unexpected mDNS install schedule: %#v", last)
	}
	for _, forbidden := range []string{"docker.io", "docker-compose-v2", "ufw", "bash"} {
		if containsArg(last.args, forbidden) {
			t.Fatalf("mDNS schedule contains unrelated argument %q: %#v", forbidden, last)
		}
	}
}

func TestCatalogPackageMDNSReadyRequiresBothFixedPackages(t *testing.T) {
	runner := &packageRecordingRunner{hook: func(path string, args []string) (string, error) {
		if path == systemctlPath {
			return "LoadState=not-found\nActiveState=inactive\n", errors.New("not found")
		}
		if path == dpkgQueryPath {
			return "avahi-daemon=installed\nlibnss-mdns=installed\n", nil
		}
		return "", nil
	}}
	manager := newPackageManager(t, runner, "ID=ubuntu\nVERSION_ID=26.04\n")
	state, err := manager.FeatureStatus(context.Background(), PackageFeatureMDNS)
	if err != nil || state.Status != "ready" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	last := runner.commands[len(runner.commands)-1]
	if !reflect.DeepEqual(last.args, []string{"-W", "-f=${Package}=${db:Status-Status}\\n", "avahi-daemon", "libnss-mdns"}) {
		t.Fatalf("unexpected mDNS query: %#v", last)
	}
}

func TestCatalogPackageDryRunAndUnsupportedReleaseFailClosed(t *testing.T) {
	runner := &packageRecordingRunner{}
	manager := newPackageManager(t, runner, "ID=ubuntu\nVERSION_ID=24.04\n")
	state, err := manager.EnsureFeature(context.Background(), PackageFeatureDockerRuntime, true)
	if err != nil || state.Status != "validated" || len(runner.commands) != 0 {
		t.Fatalf("state/error/commands=%#v/%v/%#v", state, err, runner.commands)
	}
	unsupported := newPackageManager(t, runner, "ID=debian\nVERSION_ID=13\n")
	if _, err := unsupported.EnsureFeature(context.Background(), PackageFeatureDockerRuntime, true); err == nil {
		t.Fatal("expected unsupported operating system to be rejected")
	}
	if _, err := manager.EnsureFeature(context.Background(), PackageFeature("docker_runtime;reboot"), true); err == nil {
		t.Fatal("expected unknown feature to be rejected")
	}
}

func TestCatalogPackageEnsureRetriesOnlyFailedFixedInstallStage(t *testing.T) {
	runner := &packageRecordingRunner{hook: func(path string, args []string) (string, error) {
		if path == systemctlPath && len(args) > 0 && args[0] == "show" && args[len(args)-1] == dockerInstallUnit {
			return "LoadState=loaded\nActiveState=failed\nSubState=failed\nResult=exit-code\n", errors.New("failed")
		}
		if path == systemctlPath && reflect.DeepEqual(args, []string{"stop", dockerInstallUnit}) {
			return "", nil
		}
		if path == systemdRunPath {
			return "", nil
		}
		return "", errors.New("unexpected command")
	}}
	manager := newPackageManager(t, runner, "ID=ubuntu\nVERSION_ID=24.04\n")
	state, err := manager.EnsureFeature(context.Background(), PackageFeatureDockerRuntime, false)
	if err != nil || state.Status != "installing" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	// show(failed), dpkg-query for docker.io and for the Docker CE set (neither installed),
	// then the retry: show, stop, schedule.
	if len(runner.commands) != 6 || runner.commands[1].path != dpkgQueryPath || runner.commands[2].path != dpkgQueryPath ||
		!reflect.DeepEqual(runner.commands[4], recordedCommand{path: systemctlPath, args: []string{"stop", dockerInstallUnit}}) {
		t.Fatalf("unexpected retry commands: %#v", runner.commands)
	}
	last := runner.commands[5]
	if last.path != systemdRunPath || !hasArgsSuffix(last.args, aptGetPath, "-o", "DPkg::Lock::Timeout=300", "install", "-y", "docker.io", "docker-compose-v2") {
		t.Fatalf("unexpected retry schedule: %#v", last)
	}
}

// dockerCEHost answers dpkg-query like a host with Docker CE from Docker's repository: docker.io
// is unknown to dpkg (non-zero exit), docker-ce and docker-compose-plugin are installed.
func dockerCEHost(args []string) (string, error) {
	if containsArg(args, "docker.io") {
		return "docker-compose-v2=not-installed\n", errors.New("dpkg-query: no packages found matching docker.io")
	}
	if containsArg(args, "docker-ce") && containsArg(args, "docker-compose-plugin") {
		return "docker-ce=installed\ndocker-compose-plugin=installed\n", nil
	}
	return "", errors.New("unexpected dpkg-query")
}

func TestCatalogPackageDockerReadyWithDockerCEPackages(t *testing.T) {
	runner := &packageRecordingRunner{hook: func(path string, args []string) (string, error) {
		if path == systemctlPath {
			return "LoadState=not-found\nActiveState=inactive\n", errors.New("not found")
		}
		if path == dpkgQueryPath {
			return dockerCEHost(args)
		}
		return "", errors.New("unexpected command")
	}}
	manager := newPackageManager(t, runner, "ID=ubuntu\nVERSION_ID=24.04\n")
	state, err := manager.EnsureFeature(context.Background(), PackageFeatureDockerRuntime, false)
	if err != nil || state.Status != "ready" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	for _, command := range runner.commands {
		if command.path == systemdRunPath {
			t.Fatalf("Docker CE host must not schedule apt: %#v", runner.commands)
		}
	}
}

func TestCatalogPackageFailedInstallIsCollectedWhenDockerCEProvidesDocker(t *testing.T) {
	// The loop seen on a production node (12/10/2026): the docker.io install failed against
	// Docker CE and was rescheduled every second. Now the failed unit is collected and the
	// feature is ready.
	stopped := false
	runner := &packageRecordingRunner{hook: func(path string, args []string) (string, error) {
		if path == systemctlPath && len(args) > 0 && args[0] == "show" && args[len(args)-1] == dockerInstallUnit {
			if stopped {
				return "LoadState=not-found\nActiveState=inactive\n", errors.New("not found")
			}
			return "LoadState=loaded\nActiveState=failed\nSubState=failed\nResult=exit-code\n", errors.New("failed")
		}
		if path == systemctlPath && len(args) > 0 && args[0] == "show" {
			return "LoadState=not-found\nActiveState=inactive\n", errors.New("not found")
		}
		if path == systemctlPath && reflect.DeepEqual(args, []string{"stop", dockerInstallUnit}) {
			stopped = true
			return "", nil
		}
		if path == dpkgQueryPath {
			return dockerCEHost(args)
		}
		return "", errors.New("unexpected command")
	}}
	manager := newPackageManager(t, runner, "ID=ubuntu\nVERSION_ID=24.04\n")
	state, err := manager.EnsureFeature(context.Background(), PackageFeatureDockerRuntime, false)
	if err != nil || state.Status != "ready" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	if !stopped {
		t.Fatalf("failed install unit was not collected: %#v", runner.commands)
	}
	for _, command := range runner.commands {
		if command.path == systemdRunPath {
			t.Fatalf("must not reschedule apt: %#v", runner.commands)
		}
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
