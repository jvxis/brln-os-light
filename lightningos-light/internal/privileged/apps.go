package privileged

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"lightningos-light/internal/appmanifest"
)

const (
	defaultAppsRoot   = "/var/lib/lightningos/apps"
	dockerPath        = "/usr/bin/docker"
	dockerComposePath = "/usr/bin/docker-compose"
)

type ComposeAppManager struct {
	Runner   CommandRunner
	AppsRoot string
	TempRoot string
}

type composeAppSnapshot struct {
	root        string
	composePath string
	envPath     string
}

var dockerCPUPercentPattern = regexp.MustCompile(`^([0-9]+(?:[.,][0-9]+)?)[[:space:]]*%$`)

func NewComposeAppManager(runner CommandRunner) *ComposeAppManager {
	return &ComposeAppManager{Runner: runner, AppsRoot: defaultAppsRoot}
}

func (manager *ComposeAppManager) EnsureDockerRuntime(ctx context.Context, dryRun bool) (DockerRuntimeState, error) {
	var state DockerRuntimeState
	if manager == nil || manager.Runner == nil {
		return state, errors.New("compose app manager is unavailable")
	}
	if dryRun {
		return DockerRuntimeState{Status: "validated"}, nil
	}
	state, err := manager.DockerRuntimeStatus(ctx)
	if err != nil || state.Status == "ready" {
		return state, err
	}
	if state.Status == "failed" {
		return state, errors.New("docker runtime is unavailable")
	}
	if state.Status == "starting" {
		return state, nil
	}
	if _, err := manager.Runner.Run(ctx, systemctlPath, "start", "--no-block", "docker"); err != nil {
		return state, errors.New("docker runtime is unavailable")
	}
	return DockerRuntimeState{Status: "starting"}, nil
}

func (manager *ComposeAppManager) DockerRuntimeStatus(ctx context.Context) (DockerRuntimeState, error) {
	var state DockerRuntimeState
	if manager == nil || manager.Runner == nil {
		return state, errors.New("compose app manager is unavailable")
	}
	if _, err := manager.Runner.Run(ctx, dockerPath, "info"); err == nil {
		if _, _, composeErr := manager.resolveCompose(ctx); composeErr != nil {
			return state, composeErr
		}
		return DockerRuntimeState{Status: "ready"}, nil
	}
	output, err := manager.Runner.Run(ctx, systemctlPath, "is-active", "docker")
	switch strings.TrimSpace(output) {
	case "active", "activating":
		return DockerRuntimeState{Status: "starting"}, nil
	case "inactive":
		return DockerRuntimeState{Status: "stopped"}, nil
	case "failed":
		return DockerRuntimeState{Status: "failed"}, nil
	default:
		if err != nil {
			return DockerRuntimeState{Status: "failed"}, nil
		}
		return state, errors.New("docker runtime state is invalid")
	}
}

func (manager *ComposeAppManager) PrepareImage(ctx context.Context, appID string, variant appmanifest.CPUMinerImageVariant, dryRun bool) (AppImageState, error) {
	var state AppImageState
	image, unit, err := validatedCPUMinerImage(appID, variant)
	if err != nil {
		return state, err
	}
	if manager == nil || manager.Runner == nil {
		return state, errors.New("compose app manager is unavailable")
	}
	if dryRun {
		return AppImageState{Status: "validated"}, nil
	}
	state, err = manager.imageStatus(ctx, image, unit)
	if err != nil || state.Status == "ready" || state.Status == "preparing" {
		return state, err
	}
	if state.Status == "failed" {
		return state, errors.New("app image preparation previously failed")
	}
	args := []string{
		"--quiet",
		"--collect",
		"--unit=" + unit,
		"--property=Type=exec",
		"--property=RuntimeMaxSec=10min",
		dockerPath,
		"pull",
		image,
	}
	if _, err := manager.Runner.Run(ctx, systemdRunPath, args...); err != nil {
		return state, errors.New("app image preparation could not be scheduled")
	}
	return AppImageState{Status: "preparing"}, nil
}

func (manager *ComposeAppManager) ImageStatus(ctx context.Context, appID string, variant appmanifest.CPUMinerImageVariant) (AppImageState, error) {
	var state AppImageState
	image, unit, err := validatedCPUMinerImage(appID, variant)
	if err != nil {
		return state, err
	}
	if manager == nil || manager.Runner == nil {
		return state, errors.New("compose app manager is unavailable")
	}
	return manager.imageStatus(ctx, image, unit)
}

func (manager *ComposeAppManager) ProbeImage(ctx context.Context, appID string, variant appmanifest.CPUMinerImageVariant, dryRun bool) (AppImageProbe, error) {
	var probe AppImageProbe
	image, _, err := validatedCPUMinerImage(appID, variant)
	if err != nil {
		return probe, err
	}
	if manager == nil || manager.Runner == nil {
		return probe, errors.New("compose app manager is unavailable")
	}
	if dryRun {
		return probe, nil
	}
	if _, err := manager.Runner.Run(ctx, dockerPath, "image", "inspect", image); err != nil {
		return probe, errors.New("app image is not available locally")
	}
	_, err = manager.Runner.Run(ctx, dockerPath, "run", "--rm", image,
		"cpuminer", "--algo", "sha256d", "--benchmark", "--time-limit", "2")
	probe.Runnable = err == nil
	return probe, nil
}

func (manager *ComposeAppManager) imageStatus(ctx context.Context, image string, unit string) (AppImageState, error) {
	if _, err := manager.Runner.Run(ctx, dockerPath, "image", "inspect", image); err == nil {
		return AppImageState{Status: "ready"}, nil
	}
	output, showErr := manager.Runner.Run(ctx, systemctlPath, "show",
		"--property=LoadState", "--property=ActiveState", "--property=SubState", "--property=Result", "--no-pager", unit)
	values := parseSystemdProperties(output)
	if values["LoadState"] == "not-found" || (showErr != nil && values["LoadState"] == "") {
		return AppImageState{Status: "absent"}, nil
	}
	switch values["ActiveState"] {
	case "active", "activating", "reloading":
		return AppImageState{Status: "preparing"}, nil
	case "failed", "inactive", "deactivating":
		return AppImageState{Status: "failed"}, nil
	default:
		return AppImageState{}, errors.New("app image preparation state is invalid")
	}
}

func validatedCPUMinerImage(appID string, variant appmanifest.CPUMinerImageVariant) (string, string, error) {
	if appID != appmanifest.CPUMinerID {
		return "", "", errors.New("app manifest is not allowed")
	}
	image, err := appmanifest.CPUMinerImageForVariant(variant)
	if err != nil {
		return "", "", err
	}
	units := map[appmanifest.CPUMinerImageVariant]string{
		appmanifest.CPUMinerImageBaseline:   "lightningos-cpuminer-image-baseline",
		appmanifest.CPUMinerImageFastPinned: "lightningos-cpuminer-image-fast-pinned",
		appmanifest.CPUMinerImageFastLatest: "lightningos-cpuminer-image-fast-latest",
	}
	unit, ok := units[variant]
	if !ok {
		return "", "", errors.New("app image variant is not allowed")
	}
	return image, unit, nil
}

func parseSystemdProperties(output string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && key != "" {
			values[key] = value
		}
	}
	return values
}

// Lifecycle validates the selected catalog manifest and its environment,
// snapshots both into a broker-owned directory, and executes one fixed Docker
// Compose action. No path, service name, image, or argument comes from the
// request.
func (manager *ComposeAppManager) Lifecycle(ctx context.Context, appID string, action AppLifecycleAction, dryRun bool) error {
	if manager == nil || manager.Runner == nil {
		return errors.New("compose app manager is unavailable")
	}
	if appID != appmanifest.CPUMinerID {
		return errors.New("app manifest is not allowed")
	}
	if action != AppLifecycleStart && action != AppLifecycleStop {
		return errors.New("app lifecycle action is not allowed")
	}
	composeRaw, envRaw, err := manager.validatedCPUMinerFiles()
	if err != nil {
		return err
	}
	if dryRun {
		return nil
	}
	if action == AppLifecycleStart {
		image, err := appmanifest.CPUMinerImage(envRaw)
		if err != nil {
			return errors.New("app image selection is invalid")
		}
		if _, err := manager.Runner.Run(ctx, dockerPath, "image", "inspect", image); err != nil {
			return errors.New("app image is not available locally")
		}
	}

	snapshot, cleanup, err := manager.createSnapshot(composeRaw, envRaw)
	if err != nil {
		return err
	}
	defer cleanup()

	commandPath, prefix, err := manager.resolveCompose(ctx)
	if err != nil {
		return err
	}
	args := append(prefix,
		"--env-file", snapshot.envPath,
		"--project-name", appmanifest.CPUMinerProject,
		"--project-directory", snapshot.root,
		"-f", snapshot.composePath,
	)
	switch action {
	case AppLifecycleStart:
		args = append(args, "up", "-d")
	case AppLifecycleStop:
		// Compose defaults to a ten-second graceful-stop window, which exceeds
		// the broker client's five-second default. CPU Miner holds no mutable
		// container state, so this manifest uses a bounded two-second window.
		args = append(args, "stop", "--timeout", "2")
	}
	if _, err := manager.Runner.Run(ctx, commandPath, args...); err != nil {
		return errors.New("app lifecycle command failed")
	}
	return nil
}

// Remove validates and snapshots the catalog manifest before executing a
// fixed Compose down action. The manager remains responsible for deleting its
// own unprivileged app files only after this method succeeds.
func (manager *ComposeAppManager) Remove(ctx context.Context, appID string, dryRun bool) error {
	if manager == nil || manager.Runner == nil {
		return errors.New("compose app manager is unavailable")
	}
	if appID != appmanifest.CPUMinerID {
		return errors.New("app manifest is not allowed")
	}
	composeRaw, envRaw, err := manager.validatedCPUMinerFiles()
	if err != nil {
		return err
	}
	if dryRun {
		return nil
	}

	snapshot, cleanup, err := manager.createSnapshot(composeRaw, envRaw)
	if err != nil {
		return err
	}
	defer cleanup()

	commandPath, prefix, err := manager.resolveCompose(ctx)
	if err != nil {
		return err
	}
	args := append(prefix,
		"--env-file", snapshot.envPath,
		"--project-name", appmanifest.CPUMinerProject,
		"--project-directory", snapshot.root,
		"-f", snapshot.composePath,
		"down", "--remove-orphans", "--timeout", "2",
	)
	if _, err := manager.Runner.Run(ctx, commandPath, args...); err != nil {
		return errors.New("app remove command failed")
	}
	return nil
}

func (manager *ComposeAppManager) Inspect(ctx context.Context, appID string) (AppInspection, error) {
	var inspection AppInspection
	if manager == nil || manager.Runner == nil {
		return inspection, errors.New("compose app manager is unavailable")
	}
	if appID != appmanifest.CPUMinerID {
		return inspection, errors.New("app manifest is not allowed")
	}
	composeRaw, envRaw, err := manager.validatedCPUMinerFiles()
	if err != nil {
		return inspection, err
	}
	snapshot, cleanup, err := manager.createSnapshot(composeRaw, envRaw)
	if err != nil {
		return inspection, err
	}
	defer cleanup()

	commandPath, prefix, err := manager.resolveCompose(ctx)
	if err != nil {
		return inspection, err
	}
	baseArgs := append(prefix,
		"--env-file", snapshot.envPath,
		"--project-name", appmanifest.CPUMinerProject,
		"--project-directory", snapshot.root,
		"-f", snapshot.composePath,
	)
	output, err := manager.Runner.Run(ctx, commandPath, append(append([]string(nil), baseArgs...), "ps", "--services", "--filter", "status=running")...)
	if err != nil {
		return inspection, errors.New("app status command failed")
	}
	inspection.Status = "stopped"
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == appmanifest.CPUMinerID {
			inspection.Status = "running"
			break
		}
	}
	if inspection.Status != "running" {
		return inspection, nil
	}

	output, err = manager.Runner.Run(ctx, commandPath, append(append([]string(nil), baseArgs...), "ps", "-q", appmanifest.CPUMinerID)...)
	if err != nil {
		return inspection, errors.New("app container lookup failed")
	}
	containerID := parseDockerContainerID(output)
	if containerID == "" {
		return inspection, errors.New("app container lookup returned an invalid ID")
	}
	output, err = manager.Runner.Run(ctx, dockerPath, "stats", "--no-stream", "--format", "{{.CPUPerc}}", containerID)
	if err == nil {
		inspection.CPUPercentRaw = parseDockerCPUPercent(output)
	}
	return inspection, nil
}

func (manager *ComposeAppManager) validatedCPUMinerFiles() ([]byte, []byte, error) {
	appsRoot := manager.AppsRoot
	if appsRoot == "" {
		appsRoot = defaultAppsRoot
	}
	appRoot := filepath.Join(appsRoot, appmanifest.CPUMinerID)
	composeRaw, err := readRegularFile(filepath.Join(appRoot, appmanifest.CPUMinerComposeFile), 64*1024)
	if err != nil {
		return nil, nil, errors.New("app compose manifest is unavailable")
	}
	if !bytes.Equal(composeRaw, []byte(appmanifest.CPUMinerCompose())) {
		return nil, nil, errors.New("app compose manifest does not match the catalog")
	}
	envRaw, err := readRegularFile(filepath.Join(appRoot, appmanifest.CPUMinerEnvFile), 16*1024)
	if err != nil {
		return nil, nil, errors.New("app environment is unavailable")
	}
	if err := appmanifest.ValidateCPUMinerEnv(envRaw); err != nil {
		return nil, nil, errors.New("app environment does not match the catalog")
	}
	return composeRaw, envRaw, nil
}

func (manager *ComposeAppManager) createSnapshot(composeRaw []byte, envRaw []byte) (composeAppSnapshot, func(), error) {
	var snapshot composeAppSnapshot
	snapshotRoot, err := os.MkdirTemp(manager.TempRoot, "lightningos-compose-")
	if err != nil {
		return snapshot, func() {}, errors.New("failed to create app execution snapshot")
	}
	cleanup := func() { _ = os.RemoveAll(snapshotRoot) }
	if err := os.Chmod(snapshotRoot, 0700); err != nil {
		cleanup()
		return snapshot, func() {}, errors.New("failed to secure app execution snapshot")
	}
	snapshot = composeAppSnapshot{
		root:        snapshotRoot,
		composePath: filepath.Join(snapshotRoot, appmanifest.CPUMinerComposeFile),
		envPath:     filepath.Join(snapshotRoot, appmanifest.CPUMinerEnvFile),
	}
	if err := os.WriteFile(snapshot.composePath, composeRaw, 0600); err != nil {
		cleanup()
		return composeAppSnapshot{}, func() {}, errors.New("failed to snapshot app compose manifest")
	}
	if err := os.WriteFile(snapshot.envPath, envRaw, 0600); err != nil {
		cleanup()
		return composeAppSnapshot{}, func() {}, errors.New("failed to snapshot app environment")
	}
	return snapshot, cleanup, nil
}

func parseDockerContainerID(output string) string {
	containerID := ""
	for _, line := range strings.Split(output, "\n") {
		candidate := strings.TrimSpace(line)
		if candidate == "" {
			continue
		}
		if containerID != "" {
			return ""
		}
		if len(candidate) < 12 || len(candidate) > 64 {
			return ""
		}
		valid := true
		for _, char := range candidate {
			if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
				valid = false
				break
			}
		}
		if !valid {
			return ""
		}
		containerID = candidate
	}
	return containerID
}

func parseDockerCPUPercent(output string) float64 {
	match := dockerCPUPercentPattern.FindStringSubmatch(strings.TrimSpace(output))
	if len(match) != 2 {
		return 0
	}
	value, err := strconv.ParseFloat(strings.ReplaceAll(match[1], ",", "."), 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func (manager *ComposeAppManager) resolveCompose(ctx context.Context) (string, []string, error) {
	if _, err := manager.Runner.Run(ctx, dockerPath, "compose", "version"); err == nil {
		return dockerPath, []string{"compose"}, nil
	}
	if _, err := manager.Runner.Run(ctx, dockerComposePath, "version"); err == nil {
		return dockerComposePath, nil, nil
	}
	return "", nil, errors.New("docker compose is unavailable")
}

func readRegularFile(path string, maxBytes int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 1 || info.Size() > maxBytes {
		return nil, fmt.Errorf("invalid regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != info.Size() {
		return nil, errors.New("file changed while reading")
	}
	return data, nil
}
