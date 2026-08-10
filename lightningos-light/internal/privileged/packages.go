package privileged

import (
	"context"
	"errors"
	"os"
	"strings"
)

const (
	defaultOSReleasePath = "/etc/os-release"
	dpkgQueryPath        = "/usr/bin/dpkg-query"
	aptGetPath           = "/usr/bin/apt-get"
	flockPath            = "/usr/bin/flock"
	packageFeatureLock   = "/run/lock/lightningos/package-feature.lock"
	dockerIndexUnit      = "lightningos-package-docker-index"
	dockerInstallUnit    = "lightningos-package-docker-install"
)

var supportedPackageOperatingSystems = map[string]struct{}{
	"ubuntu:24.04": {},
	"ubuntu:26.04": {},
}

type CatalogPackageManager struct {
	Runner        CommandRunner
	OSReleasePath string
}

type packageUnitState string

const (
	packageUnitAbsent    packageUnitState = "absent"
	packageUnitRunning   packageUnitState = "running"
	packageUnitSucceeded packageUnitState = "succeeded"
	packageUnitFailed    packageUnitState = "failed"
)

func NewCatalogPackageManager(runner CommandRunner) *CatalogPackageManager {
	return &CatalogPackageManager{Runner: runner, OSReleasePath: defaultOSReleasePath}
}

func (manager *CatalogPackageManager) EnsureFeature(ctx context.Context, feature PackageFeature, dryRun bool) (PackageFeatureState, error) {
	if err := manager.validate(feature); err != nil {
		return PackageFeatureState{}, err
	}
	if dryRun {
		return PackageFeatureState{Status: "validated"}, nil
	}
	state, err := manager.FeatureStatus(ctx, feature)
	if err != nil {
		return PackageFeatureState{}, err
	}
	switch state.Status {
	case "ready":
		if err := manager.cleanupFinishedUnits(ctx); err != nil {
			return PackageFeatureState{}, err
		}
		return state, nil
	case "indexing", "installing":
		return state, nil
	case "indexed":
		if err := manager.schedule(ctx, dockerInstallUnit, []string{
			"-o", "DPkg::Lock::Timeout=300", "install", "-y",
			"docker.io", "docker-compose-v2",
		}); err != nil {
			return PackageFeatureState{}, err
		}
		return PackageFeatureState{Status: "installing"}, nil
	case "absent":
		if err := manager.schedule(ctx, dockerIndexUnit, []string{"-o", "DPkg::Lock::Timeout=300", "update"}); err != nil {
			return PackageFeatureState{}, err
		}
		return PackageFeatureState{Status: "indexing"}, nil
	case "failed":
		return manager.retryFailedStage(ctx)
	default:
		return PackageFeatureState{}, errors.New("package feature state is invalid")
	}
}

func (manager *CatalogPackageManager) FeatureStatus(ctx context.Context, feature PackageFeature) (PackageFeatureState, error) {
	if err := manager.validate(feature); err != nil {
		return PackageFeatureState{}, err
	}
	installState, err := manager.unitState(ctx, dockerInstallUnit)
	if err != nil {
		return PackageFeatureState{}, err
	}
	switch installState {
	case packageUnitRunning:
		return PackageFeatureState{Status: "installing"}, nil
	case packageUnitSucceeded:
		if manager.dockerFeatureInstalled(ctx) {
			return PackageFeatureState{Status: "ready"}, nil
		}
		return PackageFeatureState{Status: "failed"}, nil
	case packageUnitFailed:
		return PackageFeatureState{Status: "failed"}, nil
	}
	if manager.dockerFeatureInstalled(ctx) {
		return PackageFeatureState{Status: "ready"}, nil
	}
	indexState, err := manager.unitState(ctx, dockerIndexUnit)
	if err != nil {
		return PackageFeatureState{}, err
	}
	switch indexState {
	case packageUnitRunning:
		return PackageFeatureState{Status: "indexing"}, nil
	case packageUnitSucceeded:
		return PackageFeatureState{Status: "indexed"}, nil
	case packageUnitFailed:
		return PackageFeatureState{Status: "failed"}, nil
	default:
		return PackageFeatureState{Status: "absent"}, nil
	}
}

func (manager *CatalogPackageManager) validate(feature PackageFeature) error {
	if manager == nil || manager.Runner == nil {
		return errors.New("catalog package manager is unavailable")
	}
	if feature != PackageFeatureDockerRuntime {
		return errors.New("package feature is not allowed")
	}
	path := manager.OSReleasePath
	if path == "" {
		path = defaultOSReleasePath
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return errors.New("operating system release is unavailable")
	}
	values := parseOSRelease(string(raw))
	key := values["ID"] + ":" + values["VERSION_ID"]
	if _, ok := supportedPackageOperatingSystems[key]; !ok {
		return errors.New("operating system release is not supported for package installation")
	}
	return nil
}

func (manager *CatalogPackageManager) dockerFeatureInstalled(ctx context.Context) bool {
	output, err := manager.Runner.Run(ctx, dpkgQueryPath, "-W", "-f=${Package}=${db:Status-Status}\\n", "docker.io", "docker-compose-v2")
	if err != nil {
		return false
	}
	installed := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		name, status, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && status == "installed" {
			installed[name] = true
		}
	}
	return installed["docker.io"] && installed["docker-compose-v2"]
}

func (manager *CatalogPackageManager) schedule(ctx context.Context, unit string, aptArgs []string) error {
	args := []string{
		"--quiet",
		"--no-block",
		"--collect",
		"--unit=" + unit,
		"--property=Type=oneshot",
		"--property=RemainAfterExit=yes",
		"--property=RuntimeMaxSec=15min",
		"--setenv=DEBIAN_FRONTEND=noninteractive",
		flockPath,
		"--exclusive",
		packageFeatureLock,
		aptGetPath,
	}
	args = append(args, aptArgs...)
	if _, err := manager.Runner.Run(ctx, systemdRunPath, args...); err != nil {
		return errors.New("package feature operation could not be scheduled")
	}
	return nil
}

func (manager *CatalogPackageManager) retryFailedStage(ctx context.Context) (PackageFeatureState, error) {
	installState, err := manager.unitState(ctx, dockerInstallUnit)
	if err != nil {
		return PackageFeatureState{}, err
	}
	if installState == packageUnitFailed || installState == packageUnitSucceeded {
		if _, err := manager.Runner.Run(ctx, systemctlPath, "stop", dockerInstallUnit); err != nil {
			return PackageFeatureState{}, errors.New("failed package feature unit could not be cleared")
		}
		if err := manager.schedule(ctx, dockerInstallUnit, []string{
			"-o", "DPkg::Lock::Timeout=300", "install", "-y", "docker.io", "docker-compose-v2",
		}); err != nil {
			return PackageFeatureState{}, err
		}
		return PackageFeatureState{Status: "installing"}, nil
	}
	indexState, err := manager.unitState(ctx, dockerIndexUnit)
	if err != nil {
		return PackageFeatureState{}, err
	}
	if indexState != packageUnitFailed {
		return PackageFeatureState{}, errors.New("failed package feature stage is invalid")
	}
	if _, err := manager.Runner.Run(ctx, systemctlPath, "stop", dockerIndexUnit); err != nil {
		return PackageFeatureState{}, errors.New("failed package feature unit could not be cleared")
	}
	if err := manager.schedule(ctx, dockerIndexUnit, []string{"-o", "DPkg::Lock::Timeout=300", "update"}); err != nil {
		return PackageFeatureState{}, err
	}
	return PackageFeatureState{Status: "indexing"}, nil
}

func (manager *CatalogPackageManager) cleanupFinishedUnits(ctx context.Context) error {
	for _, unit := range []string{dockerInstallUnit, dockerIndexUnit} {
		state, err := manager.unitState(ctx, unit)
		if err != nil {
			return err
		}
		if state != packageUnitSucceeded && state != packageUnitFailed {
			continue
		}
		if _, err := manager.Runner.Run(ctx, systemctlPath, "stop", unit); err != nil {
			return errors.New("package feature unit could not be collected")
		}
	}
	return nil
}

func (manager *CatalogPackageManager) unitState(ctx context.Context, unit string) (packageUnitState, error) {
	output, showErr := manager.Runner.Run(ctx, systemctlPath, "show",
		"--property=LoadState", "--property=ActiveState", "--property=SubState", "--property=Result", "--no-pager", unit)
	values := parseSystemdProperties(output)
	if values["LoadState"] == "not-found" || (showErr != nil && values["LoadState"] == "") {
		return packageUnitAbsent, nil
	}
	if values["ActiveState"] == "active" && values["SubState"] == "exited" && values["Result"] == "success" {
		return packageUnitSucceeded, nil
	}
	switch values["ActiveState"] {
	case "active", "activating", "reloading":
		return packageUnitRunning, nil
	case "failed":
		return packageUnitFailed, nil
	case "inactive":
		if values["Result"] == "success" {
			return packageUnitSucceeded, nil
		}
		return packageUnitFailed, nil
	default:
		return "", errors.New("package feature unit state is invalid")
	}
}

func parseOSRelease(raw string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || (key != "ID" && key != "VERSION_ID") {
			continue
		}
		values[key] = strings.Trim(strings.TrimSpace(value), "\"")
	}
	return values
}
