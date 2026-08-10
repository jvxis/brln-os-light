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
	if len(runner.commands) != 4 || !reflect.DeepEqual(runner.commands[2], recordedCommand{path: systemctlPath, args: []string{"stop", dockerInstallUnit}}) {
		t.Fatalf("unexpected retry commands: %#v", runner.commands)
	}
	last := runner.commands[3]
	if last.path != systemdRunPath || !hasArgsSuffix(last.args, aptGetPath, "-o", "DPkg::Lock::Timeout=300", "install", "-y", "docker.io", "docker-compose-v2") {
		t.Fatalf("unexpected retry schedule: %#v", last)
	}
}
