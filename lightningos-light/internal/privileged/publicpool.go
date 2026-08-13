package privileged

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"

	"lightningos-light/internal/appmanifest"
)

type PublicPoolPaths struct {
	SnapshotRoot      string
	DataDir           string
	ComposePath       string
	EnvPath           string
	CaddyfilePath     string
	LegacyComposePath string
}

type NativePublicPoolManager struct {
	Runner CommandRunner
	Paths  PublicPoolPaths
}

func NewNativePublicPoolManager(runner CommandRunner) *NativePublicPoolManager {
	root := filepath.Join(defaultPrivilegedAppsRoot, appmanifest.PublicPoolID)
	return &NativePublicPoolManager{Runner: runner, Paths: PublicPoolPaths{
		SnapshotRoot:      root,
		DataDir:           filepath.Join(defaultAppsDataRoot, appmanifest.PublicPoolID, "db"),
		ComposePath:       filepath.Join(root, appmanifest.PublicPoolComposeFile),
		EnvPath:           filepath.Join(root, appmanifest.PublicPoolEnvFile),
		CaddyfilePath:     filepath.Join(root, appmanifest.PublicPoolCaddyfile),
		LegacyComposePath: filepath.Join(defaultAppsRoot, appmanifest.PublicPoolID, appmanifest.PublicPoolComposeFile),
	}}
}

func (manager *NativePublicPoolManager) Status(ctx context.Context) (PublicPoolState, error) {
	if manager == nil || manager.Runner == nil {
		return PublicPoolState{}, errors.New("Public Pool command runner is unavailable")
	}
	state := PublicPoolState{Installed: manager.snapshotReady() || safeNonEmptyRegularFile(manager.Paths.LegacyComposePath), Status: "stopped"}
	if !state.Installed {
		return state, nil
	}
	output, err := manager.Runner.Run(ctx, dockerPath, "ps", "--filter", "label=com.docker.compose.project="+appmanifest.PublicPoolProject,
		"--filter", "label=com.docker.compose.service="+appmanifest.PublicPoolPrimaryService,
		"--filter", "status=running", "--format", "{{.ID}}")
	if err != nil {
		state.Status = "unknown"
		return state, errors.New("Public Pool container status failed")
	}
	if strings.TrimSpace(output) != "" {
		if parseDockerContainerID(output) == "" {
			state.Status = "unknown"
			return state, errors.New("Public Pool container identity is ambiguous")
		}
		state.Status = "running"
	}
	return state, nil
}

func (manager *NativePublicPoolManager) Ensure(_ context.Context, params PublicPoolEnsureParams, dryRun bool) (PublicPoolState, error) {
	envRaw, err := appmanifest.PublicPoolEnv(params.Runtime)
	if err != nil {
		return PublicPoolState{}, err
	}
	composeRaw, err := appmanifest.PublicPoolCompose(appmanifest.PublicPoolComposePaths{
		DataDir: manager.Paths.DataDir, CaddyfilePath: manager.Paths.CaddyfilePath,
	}, params.Runtime.BitcoinMode)
	if err != nil {
		return PublicPoolState{}, err
	}
	if dryRun {
		return PublicPoolState{Status: "validated"}, nil
	}
	for _, directory := range []string{manager.Paths.DataDir, manager.Paths.SnapshotRoot} {
		if err := ensureDirectoryTreeNoSymlink(directory, 0700); err != nil {
			return PublicPoolState{}, errors.New("Public Pool directory preparation failed")
		}
	}
	if err := preparePublicPoolWritableData(manager.Paths.DataDir); err != nil {
		return PublicPoolState{}, errors.New("Public Pool writable data preparation failed")
	}
	if err := writeAtomicRegularFile(manager.Paths.EnvPath, []byte(envRaw), 0600); err != nil {
		return PublicPoolState{}, errors.New("Public Pool environment write failed")
	}
	if err := writeAtomicRegularFile(manager.Paths.CaddyfilePath, []byte(appmanifest.PublicPoolCaddyConfig()), 0640); err != nil {
		return PublicPoolState{}, errors.New("Public Pool web configuration write failed")
	}
	if err := setPrivilegedPathGroup(manager.Paths.CaddyfilePath, appmanifest.PublicPoolContainerGID); err != nil {
		return PublicPoolState{}, errors.New("Public Pool web configuration ownership failed")
	}
	if err := writeAtomicRegularFile(manager.Paths.ComposePath, []byte(composeRaw), 0600); err != nil {
		return PublicPoolState{}, errors.New("Public Pool compose write failed")
	}
	return PublicPoolState{Installed: true, Status: "stopped"}, nil
}

func (manager *NativePublicPoolManager) Lifecycle(ctx context.Context, action AppLifecycleAction, dryRun bool) (PublicPoolState, error) {
	if action != AppLifecycleStart && action != AppLifecycleStop {
		return PublicPoolState{}, errors.New("Public Pool lifecycle action is not allowed")
	}
	if _, err := manager.validateSnapshot(); err != nil {
		return PublicPoolState{}, err
	}
	if dryRun {
		return PublicPoolState{Installed: true, Status: "validated"}, nil
	}
	if action == AppLifecycleStart {
		for _, image := range appmanifest.PublicPoolImages() {
			if _, err := manager.Runner.Run(ctx, dockerPath, "image", "inspect", image); err != nil {
				return PublicPoolState{}, errors.New("verified Public Pool image is not ready")
			}
		}
	}
	command, prefix, err := (&ComposeAppManager{Runner: manager.Runner}).resolveCompose(ctx)
	if err != nil {
		return PublicPoolState{}, err
	}
	args := append([]string(nil), prefix...)
	args = append(args, "--project-name", appmanifest.PublicPoolProject, "--project-directory", manager.Paths.SnapshotRoot,
		"-f", manager.Paths.ComposePath)
	if action == AppLifecycleStart {
		args = append(args, "up", "-d")
	} else {
		args = append(args, "stop", "--timeout", strconv.Itoa(appmanifest.PublicPoolStopTimeout))
	}
	if _, err := manager.Runner.Run(ctx, command, args...); err != nil {
		return PublicPoolState{}, errors.New("Public Pool lifecycle command failed")
	}
	return manager.Status(ctx)
}

func (manager *NativePublicPoolManager) Remove(ctx context.Context, dryRun bool) error {
	if !manager.snapshotReady() {
		if dryRun {
			return nil
		}
		return manager.removeLegacyContainers(ctx)
	}
	if _, err := manager.validateSnapshot(); err != nil {
		return err
	}
	if dryRun {
		return nil
	}
	command, prefix, err := (&ComposeAppManager{Runner: manager.Runner}).resolveCompose(ctx)
	if err != nil {
		return err
	}
	args := append([]string(nil), prefix...)
	args = append(args, "--project-name", appmanifest.PublicPoolProject, "--project-directory", manager.Paths.SnapshotRoot,
		"-f", manager.Paths.ComposePath, "down", "--remove-orphans", "--timeout", strconv.Itoa(appmanifest.PublicPoolStopTimeout))
	if _, err := manager.Runner.Run(ctx, command, args...); err != nil {
		return errors.New("Public Pool remove command failed")
	}
	return removeFixedTree(manager.Paths.SnapshotRoot, filepath.Dir(manager.Paths.SnapshotRoot))
}

func (manager *NativePublicPoolManager) EnsureFirewall(ctx context.Context, dryRun bool) (PublicPoolState, error) {
	if dryRun {
		return PublicPoolState{Status: "validated"}, nil
	}
	status, err := manager.Runner.Run(ctx, ufwPath, "status")
	if err != nil || !strings.Contains(strings.ToLower(status), "status: active") {
		return PublicPoolState{Status: "inactive"}, nil
	}
	for _, port := range []int{appmanifest.PublicPoolStratumPort, appmanifest.PublicPoolUIPort} {
		if _, err := manager.Runner.Run(ctx, ufwPath, "allow", strconv.Itoa(port)+"/tcp"); err != nil {
			return PublicPoolState{}, errors.New("Public Pool firewall preparation failed")
		}
	}
	return PublicPoolState{Status: "active", UFWActive: true}, nil
}

func (manager *NativePublicPoolManager) validateSnapshot() (appmanifest.PublicPoolRuntime, error) {
	if err := validatePublicPoolSnapshotPermissions(manager.Paths.SnapshotRoot, manager.Paths.ComposePath, manager.Paths.EnvPath, manager.Paths.CaddyfilePath); err != nil {
		return appmanifest.PublicPoolRuntime{}, err
	}
	envRaw, err := readRegularFile(manager.Paths.EnvPath, 16*1024)
	if err != nil {
		return appmanifest.PublicPoolRuntime{}, errors.New("Public Pool environment is unavailable")
	}
	runtime, err := appmanifest.ParsePublicPoolEnv(envRaw)
	if err != nil {
		return runtime, err
	}
	expected, err := appmanifest.PublicPoolCompose(appmanifest.PublicPoolComposePaths{DataDir: manager.Paths.DataDir, CaddyfilePath: manager.Paths.CaddyfilePath}, runtime.BitcoinMode)
	if err != nil {
		return runtime, err
	}
	composeRaw, err := readRegularFile(manager.Paths.ComposePath, 64*1024)
	if err != nil || string(composeRaw) != expected {
		return runtime, errors.New("Public Pool compose does not match the catalog")
	}
	caddyRaw, err := readRegularFile(manager.Paths.CaddyfilePath, 16*1024)
	if err != nil || string(caddyRaw) != appmanifest.PublicPoolCaddyConfig() {
		return runtime, errors.New("Public Pool web configuration does not match the catalog")
	}
	return runtime, nil
}

func (manager *NativePublicPoolManager) snapshotReady() bool {
	return safeNonEmptyRegularFile(manager.Paths.ComposePath) && safeNonEmptyRegularFile(manager.Paths.EnvPath) && safeNonEmptyRegularFile(manager.Paths.CaddyfilePath)
}

func (manager *NativePublicPoolManager) removeLegacyContainers(ctx context.Context) error {
	for _, service := range []string{appmanifest.PublicPoolPrimaryService, appmanifest.PublicPoolUIService} {
		output, err := manager.Runner.Run(ctx, dockerPath, "ps", "-a", "--filter", "label=com.docker.compose.project="+appmanifest.PublicPoolProject,
			"--filter", "label=com.docker.compose.service="+service, "--format", "{{.ID}}")
		if err != nil {
			return errors.New("Public Pool legacy container status failed")
		}
		if id := parseDockerContainerID(output); id != "" {
			if _, err := manager.Runner.Run(ctx, dockerPath, "rm", "-f", id); err != nil {
				return errors.New("Public Pool legacy container removal failed")
			}
		} else if strings.TrimSpace(output) != "" {
			return errors.New("Public Pool legacy container identity is ambiguous")
		}
	}
	return removeFixedTree(manager.Paths.SnapshotRoot, filepath.Dir(manager.Paths.SnapshotRoot))
}
