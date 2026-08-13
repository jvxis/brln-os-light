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
	mdnsIndexUnit        = "lightningos-package-mdns-index"
	mdnsInstallUnit      = "lightningos-package-mdns-install"
)

var supportedPackageOperatingSystems = map[string]struct{}{
	"ubuntu:24.04": {},
	"ubuntu:26.04": {},
}

type CatalogPackageManager struct {
	Runner        CommandRunner
	OSReleasePath string
}

type packageFeatureSpec struct {
	IndexUnit   string
	InstallUnit string
	Packages    []string
}

var packageFeatureCatalog = map[PackageFeature]packageFeatureSpec{
	PackageFeatureDockerRuntime: {IndexUnit: dockerIndexUnit, InstallUnit: dockerInstallUnit, Packages: []string{"docker.io", "docker-compose-v2"}},
	PackageFeatureMDNS:          {IndexUnit: mdnsIndexUnit, InstallUnit: mdnsInstallUnit, Packages: []string{"avahi-daemon", "libnss-mdns"}},
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
	spec, err := manager.validate(feature)
	if err != nil {
		return PackageFeatureState{}, err
	}
	if dryRun {
		return PackageFeatureState{Status: "validated"}, nil
	}
	state, err := manager.featureStatus(ctx, spec)
	if err != nil {
		return PackageFeatureState{}, err
	}
	switch state.Status {
	case "ready":
		if err := manager.cleanupFinishedUnits(ctx, spec); err != nil {
			return PackageFeatureState{}, err
		}
		return state, nil
	case "indexing", "installing":
		return state, nil
	case "indexed":
		args := []string{"-o", "DPkg::Lock::Timeout=300", "install", "-y"}
		args = append(args, spec.Packages...)
		if err := manager.schedule(ctx, spec.InstallUnit, args); err != nil {
			return PackageFeatureState{}, err
		}
		return PackageFeatureState{Status: "installing"}, nil
	case "absent":
		if err := manager.schedule(ctx, spec.IndexUnit, []string{"-o", "DPkg::Lock::Timeout=300", "update"}); err != nil {
			return PackageFeatureState{}, err
		}
		return PackageFeatureState{Status: "indexing"}, nil
	case "failed":
		return manager.retryFailedStage(ctx, spec)
	default:
		return PackageFeatureState{}, errors.New("package feature state is invalid")
	}
}

func (manager *CatalogPackageManager) FeatureStatus(ctx context.Context, feature PackageFeature) (PackageFeatureState, error) {
	spec, err := manager.validate(feature)
	if err != nil {
		return PackageFeatureState{}, err
	}
	return manager.featureStatus(ctx, spec)
}

func (manager *CatalogPackageManager) featureStatus(ctx context.Context, spec packageFeatureSpec) (PackageFeatureState, error) {
	installState, err := manager.unitState(ctx, spec.InstallUnit)
	if err != nil {
		return PackageFeatureState{}, err
	}
	switch installState {
	case packageUnitRunning:
		return PackageFeatureState{Status: "installing"}, nil
	case packageUnitSucceeded:
		if manager.featureInstalled(ctx, spec) {
			return PackageFeatureState{Status: "ready"}, nil
		}
		return PackageFeatureState{Status: "failed"}, nil
	case packageUnitFailed:
		return PackageFeatureState{Status: "failed"}, nil
	}
	if manager.featureInstalled(ctx, spec) {
		return PackageFeatureState{Status: "ready"}, nil
	}
	indexState, err := manager.unitState(ctx, spec.IndexUnit)
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

func (manager *CatalogPackageManager) validate(feature PackageFeature) (packageFeatureSpec, error) {
	if manager == nil || manager.Runner == nil {
		return packageFeatureSpec{}, errors.New("catalog package manager is unavailable")
	}
	spec, ok := packageFeatureCatalog[feature]
	if !ok || len(spec.Packages) == 0 || spec.IndexUnit == "" || spec.InstallUnit == "" {
		return packageFeatureSpec{}, errors.New("package feature is not allowed")
	}
	path := manager.OSReleasePath
	if path == "" {
		path = defaultOSReleasePath
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return packageFeatureSpec{}, errors.New("operating system release is unavailable")
	}
	values := parseOSRelease(string(raw))
	key := values["ID"] + ":" + values["VERSION_ID"]
	if _, ok := supportedPackageOperatingSystems[key]; !ok {
		return packageFeatureSpec{}, errors.New("operating system release is not supported for package installation")
	}
	return spec, nil
}

func (manager *CatalogPackageManager) featureInstalled(ctx context.Context, spec packageFeatureSpec) bool {
	args := []string{"-W", "-f=${Package}=${db:Status-Status}\\n"}
	args = append(args, spec.Packages...)
	output, err := manager.Runner.Run(ctx, dpkgQueryPath, args...)
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
	for _, packageName := range spec.Packages {
		if !installed[packageName] {
			return false
		}
	}
	return true
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

func (manager *CatalogPackageManager) retryFailedStage(ctx context.Context, spec packageFeatureSpec) (PackageFeatureState, error) {
	installState, err := manager.unitState(ctx, spec.InstallUnit)
	if err != nil {
		return PackageFeatureState{}, err
	}
	if installState == packageUnitFailed || installState == packageUnitSucceeded {
		if _, err := manager.Runner.Run(ctx, systemctlPath, "stop", spec.InstallUnit); err != nil {
			return PackageFeatureState{}, errors.New("failed package feature unit could not be cleared")
		}
		args := []string{"-o", "DPkg::Lock::Timeout=300", "install", "-y"}
		args = append(args, spec.Packages...)
		if err := manager.schedule(ctx, spec.InstallUnit, args); err != nil {
			return PackageFeatureState{}, err
		}
		return PackageFeatureState{Status: "installing"}, nil
	}
	indexState, err := manager.unitState(ctx, spec.IndexUnit)
	if err != nil {
		return PackageFeatureState{}, err
	}
	if indexState != packageUnitFailed {
		return PackageFeatureState{}, errors.New("failed package feature stage is invalid")
	}
	if _, err := manager.Runner.Run(ctx, systemctlPath, "stop", spec.IndexUnit); err != nil {
		return PackageFeatureState{}, errors.New("failed package feature unit could not be cleared")
	}
	if err := manager.schedule(ctx, spec.IndexUnit, []string{"-o", "DPkg::Lock::Timeout=300", "update"}); err != nil {
		return PackageFeatureState{}, err
	}
	return PackageFeatureState{Status: "indexing"}, nil
}

func (manager *CatalogPackageManager) cleanupFinishedUnits(ctx context.Context, spec packageFeatureSpec) error {
	for _, unit := range []string{spec.InstallUnit, spec.IndexUnit} {
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
