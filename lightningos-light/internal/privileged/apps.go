package privileged

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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

func NewComposeAppManager(runner CommandRunner) *ComposeAppManager {
	return &ComposeAppManager{Runner: runner, AppsRoot: defaultAppsRoot}
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
	appsRoot := manager.AppsRoot
	if appsRoot == "" {
		appsRoot = defaultAppsRoot
	}
	appRoot := filepath.Join(appsRoot, appmanifest.CPUMinerID)
	composePath := filepath.Join(appRoot, appmanifest.CPUMinerComposeFile)
	envPath := filepath.Join(appRoot, appmanifest.CPUMinerEnvFile)

	composeRaw, err := readRegularFile(composePath, 64*1024)
	if err != nil {
		return errors.New("app compose manifest is unavailable")
	}
	if !bytes.Equal(composeRaw, []byte(appmanifest.CPUMinerCompose())) {
		return errors.New("app compose manifest does not match the catalog")
	}
	envRaw, err := readRegularFile(envPath, 16*1024)
	if err != nil {
		return errors.New("app environment is unavailable")
	}
	if err := appmanifest.ValidateCPUMinerEnv(envRaw); err != nil {
		return errors.New("app environment does not match the catalog")
	}
	if dryRun {
		return nil
	}

	snapshotRoot, err := os.MkdirTemp(manager.TempRoot, "lightningos-compose-")
	if err != nil {
		return errors.New("failed to create app execution snapshot")
	}
	defer os.RemoveAll(snapshotRoot)
	if err := os.Chmod(snapshotRoot, 0700); err != nil {
		return errors.New("failed to secure app execution snapshot")
	}
	snapshotCompose := filepath.Join(snapshotRoot, appmanifest.CPUMinerComposeFile)
	snapshotEnv := filepath.Join(snapshotRoot, appmanifest.CPUMinerEnvFile)
	if err := os.WriteFile(snapshotCompose, composeRaw, 0600); err != nil {
		return errors.New("failed to snapshot app compose manifest")
	}
	if err := os.WriteFile(snapshotEnv, envRaw, 0600); err != nil {
		return errors.New("failed to snapshot app environment")
	}

	commandPath, prefix, err := manager.resolveCompose(ctx)
	if err != nil {
		return err
	}
	args := append(prefix,
		"--env-file", snapshotEnv,
		"--project-name", appmanifest.CPUMinerProject,
		"--project-directory", snapshotRoot,
		"-f", snapshotCompose,
	)
	switch action {
	case AppLifecycleStart:
		args = append(args, "up", "-d")
	case AppLifecycleStop:
		args = append(args, "stop")
	}
	if _, err := manager.Runner.Run(ctx, commandPath, args...); err != nil {
		return errors.New("app lifecycle command failed")
	}
	return nil
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
