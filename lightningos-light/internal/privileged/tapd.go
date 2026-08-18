package privileged

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"lightningos-light/internal/appmanifest"
)

const tapdAdminMacaroonPath = "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon"

type TapdPaths struct {
	SnapshotRoot      string
	DataDir           string
	ComposePath       string
	ConfigPath        string
	LNDDir            string
	TLSCertPath       string
	MacaroonPath      string
	LegacyComposePath string
}

type NativeTapdManager struct {
	Runner CommandRunner
	Paths  TapdPaths
}

func NewNativeTapdManager(runner CommandRunner) *NativeTapdManager {
	root := filepath.Join(defaultPrivilegedAppsRoot, appmanifest.TapdID)
	lndDir := filepath.Join(root, appmanifest.TapdLNDDir)
	return &NativeTapdManager{Runner: runner, Paths: TapdPaths{
		SnapshotRoot:      root,
		DataDir:           filepath.Join(defaultAppsDataRoot, appmanifest.TapdID, "data"),
		ComposePath:       filepath.Join(root, appmanifest.TapdComposeFile),
		ConfigPath:        filepath.Join(root, appmanifest.TapdConfigFile),
		LNDDir:            lndDir,
		TLSCertPath:       filepath.Join(lndDir, appmanifest.TapdTLSCertFile),
		MacaroonPath:      filepath.Join(lndDir, appmanifest.TapdMacaroonFile),
		LegacyComposePath: filepath.Join(defaultAppsRoot, appmanifest.TapdID, appmanifest.TapdComposeFile),
	}}
}

func (manager *NativeTapdManager) Status(ctx context.Context) (TapdState, error) {
	if manager == nil || manager.Runner == nil {
		return TapdState{}, errors.New("Tapd command runner is unavailable")
	}
	state := TapdState{
		Installed:      (safeNonEmptyRegularFile(manager.Paths.ComposePath) && safeNonEmptyRegularFile(manager.Paths.ConfigPath)) || safeNonEmptyRegularFile(manager.Paths.LegacyComposePath),
		Status:         "stopped",
		HasLNDMacaroon: safeNonEmptyRegularFile(manager.Paths.MacaroonPath),
	}
	conflict, err := manager.InterceptorConflict(ctx)
	if err != nil {
		return state, err
	}
	state.InterceptorConflict = conflict
	if !state.Installed {
		return state, nil
	}
	containerID, err := manager.runningContainerID(ctx)
	if err != nil {
		state.Status = "unknown"
		return state, errors.New("Tapd container status failed")
	}
	if containerID != "" {
		state.Status = "running"
	}
	return state, nil
}

func (manager *NativeTapdManager) InterceptorConflict(ctx context.Context) (bool, error) {
	output, err := manager.Runner.Run(ctx, dockerPath, "ps", "--filter", "label=com.docker.compose.service=gatewayd",
		"--filter", "status=running", "--format", "{{.Names}}")
	if err != nil {
		return false, errors.New("HTLC interceptor status failed")
	}
	return singleContainerName(output) != "", nil
}

func singleContainerName(output string) string {
	name := ""
	for _, line := range strings.Split(output, "\n") {
		candidate := strings.TrimSpace(line)
		if candidate == "" {
			continue
		}
		if name != "" {
			return ""
		}
		name = candidate
	}
	return name
}

func (manager *NativeTapdManager) runningContainerID(ctx context.Context) (string, error) {
	output, err := manager.Runner.Run(ctx, dockerPath, "ps", "--filter", "label=com.docker.compose.project=tapd",
		"--filter", "label=com.docker.compose.service=tapd", "--filter", "status=running", "--format", "{{.ID}}")
	if err != nil {
		return "", err
	}
	containerID := parseDockerContainerID(output)
	if strings.TrimSpace(output) != "" && containerID == "" {
		return "", errors.New("Tapd container identity is ambiguous")
	}
	return containerID, nil
}

func (manager *NativeTapdManager) Ensure(_ context.Context, params TapdEnsureParams, dryRun bool) (TapdState, error) {
	config, err := appmanifest.TapdConfig(params.DatabasePassword)
	if err != nil {
		return TapdState{}, err
	}
	if err := validateTapdTLSCertificate(params.LNDTLSCertificate); err != nil {
		return TapdState{}, err
	}
	if len(params.LNDMacaroon) > 64*1024 {
		return TapdState{}, errors.New("Tapd LND credential is invalid")
	}
	if len(params.LNDMacaroon) > 0 {
		if admin, readErr := readRegularFile(tapdAdminMacaroonPath, 64*1024); readErr == nil && bytes.Equal(admin, params.LNDMacaroon) {
			return TapdState{}, errors.New("Tapd must not use the LND admin macaroon")
		}
	}
	if len(params.LNDMacaroon) == 0 && !safeNonEmptyRegularFile(manager.Paths.MacaroonPath) {
		return TapdState{}, errors.New("Tapd dedicated LND credential is required")
	}
	compose := appmanifest.TapdCompose(appmanifest.TapdComposePaths{
		DataDir: manager.Paths.DataDir, ConfigPath: manager.Paths.ConfigPath,
		TLSCertPath: manager.Paths.TLSCertPath, MacaroonPath: manager.Paths.MacaroonPath,
	})
	if dryRun {
		return TapdState{Status: "validated", HasLNDMacaroon: len(params.LNDMacaroon) > 0 || safeNonEmptyRegularFile(manager.Paths.MacaroonPath)}, nil
	}
	for _, directory := range []struct {
		path string
		mode os.FileMode
	}{{manager.Paths.DataDir, 0700}, {manager.Paths.SnapshotRoot, 0700}, {manager.Paths.LNDDir, 0700}} {
		if err := ensureDirectoryTreeNoSymlink(directory.path, directory.mode); err != nil {
			return TapdState{}, errors.New("Tapd directory preparation failed")
		}
	}
	if err := writeAtomicRegularFile(manager.Paths.ConfigPath, []byte(config), 0600); err != nil {
		return TapdState{}, errors.New("Tapd configuration write failed")
	}
	if err := writeAtomicRegularFile(manager.Paths.TLSCertPath, params.LNDTLSCertificate, 0600); err != nil {
		return TapdState{}, errors.New("Tapd LND certificate write failed")
	}
	if len(params.LNDMacaroon) > 0 {
		if err := writeAtomicRegularFile(manager.Paths.MacaroonPath, params.LNDMacaroon, 0600); err != nil {
			return TapdState{}, errors.New("Tapd LND credential write failed")
		}
	}
	if err := writeAtomicRegularFile(manager.Paths.ComposePath, []byte(compose), 0600); err != nil {
		return TapdState{}, errors.New("Tapd compose write failed")
	}
	return TapdState{Installed: true, Status: "stopped", HasLNDMacaroon: true}, nil
}

func (manager *NativeTapdManager) Lifecycle(ctx context.Context, action AppLifecycleAction, dryRun bool) (TapdState, error) {
	if action != AppLifecycleStart && action != AppLifecycleStop {
		return TapdState{}, errors.New("Tapd lifecycle action is not allowed")
	}
	// Tapd installs created before the privileged snapshot was introduced only
	// have the manager-owned legacy declaration. Keep start fail-closed so it
	// must adopt the hardened declaration first, but allow stop to target the
	// exact catalog container by its fixed Compose labels. This is required for
	// safely quiescing legacy Bitcoin consumers before replacing the historical
	// Bitcoin network; no user-controlled path, name, or argument reaches Docker.
	if action == AppLifecycleStop &&
		(!safeNonEmptyRegularFile(manager.Paths.ComposePath) || !safeNonEmptyRegularFile(manager.Paths.MacaroonPath)) &&
		safeNonEmptyRegularFile(manager.Paths.LegacyComposePath) {
		return manager.stopLegacy(ctx, dryRun)
	}
	if !safeNonEmptyRegularFile(manager.Paths.ComposePath) || !safeNonEmptyRegularFile(manager.Paths.MacaroonPath) {
		return TapdState{}, errors.New("Tapd is not ready")
	}
	if action == AppLifecycleStart {
		conflict, err := manager.InterceptorConflict(ctx)
		if err != nil {
			return TapdState{}, err
		}
		if conflict {
			return TapdState{}, errors.New("Fedimint Lightning Gateway holds the LND HTLC interceptor")
		}
	}
	if dryRun {
		return TapdState{Status: "validated", HasLNDMacaroon: true}, nil
	}
	if action == AppLifecycleStart {
		if _, err := manager.Runner.Run(ctx, dockerPath, "image", "inspect", appmanifest.TapdImage); err != nil {
			return TapdState{}, errors.New("verified Tapd image is not ready")
		}
	}
	command, prefix, err := (&ComposeAppManager{Runner: manager.Runner}).resolveCompose(ctx)
	if err != nil {
		return TapdState{}, err
	}
	args := append([]string(nil), prefix...)
	args = append(args, "--project-name", appmanifest.TapdProject, "--project-directory", manager.Paths.SnapshotRoot, "-f", manager.Paths.ComposePath)
	if action == AppLifecycleStart {
		args = append(args, "up", "-d")
	} else {
		args = append(args, "stop", "--timeout", strconv.Itoa(appmanifest.TapdStopTimeout))
	}
	if _, err := manager.Runner.Run(ctx, command, args...); err != nil {
		return TapdState{}, errors.New("Tapd lifecycle command failed")
	}
	return manager.Status(ctx)
}

func (manager *NativeTapdManager) stopLegacy(ctx context.Context, dryRun bool) (TapdState, error) {
	containerID, err := manager.runningContainerID(ctx)
	if err != nil {
		return TapdState{}, errors.New("Tapd legacy container status failed")
	}
	if dryRun || containerID == "" {
		return TapdState{Installed: true, Status: "stopped"}, nil
	}
	if _, err := manager.Runner.Run(ctx, dockerPath, "stop", "--time", strconv.Itoa(appmanifest.TapdStopTimeout), containerID); err != nil {
		return TapdState{}, errors.New("Tapd legacy stop command failed")
	}
	return TapdState{Installed: true, Status: "stopped"}, nil
}

func (manager *NativeTapdManager) Remove(ctx context.Context, dryRun bool) error {
	if !safeNonEmptyRegularFile(manager.Paths.ComposePath) {
		if dryRun {
			return nil
		}
		output, err := manager.Runner.Run(ctx, dockerPath, "ps", "-a", "--filter", "label=com.docker.compose.project=tapd",
			"--filter", "label=com.docker.compose.service=tapd", "--format", "{{.ID}}")
		if err != nil {
			return errors.New("Tapd legacy container status failed")
		}
		containerID := parseDockerContainerID(output)
		if strings.TrimSpace(output) != "" && containerID == "" {
			return errors.New("Tapd legacy container identity is ambiguous")
		}
		if containerID != "" {
			if _, err := manager.Runner.Run(ctx, dockerPath, "rm", "-f", containerID); err != nil {
				return errors.New("Tapd legacy container removal failed")
			}
		}
		return removeFixedTree(manager.Paths.SnapshotRoot, filepath.Dir(manager.Paths.SnapshotRoot))
	}
	if dryRun {
		return nil
	}
	command, prefix, err := (&ComposeAppManager{Runner: manager.Runner}).resolveCompose(ctx)
	if err != nil {
		return err
	}
	args := append([]string(nil), prefix...)
	args = append(args, "--project-name", appmanifest.TapdProject, "--project-directory", manager.Paths.SnapshotRoot,
		"-f", manager.Paths.ComposePath, "down", "--remove-orphans", "--timeout", strconv.Itoa(appmanifest.TapdStopTimeout))
	if _, err := manager.Runner.Run(ctx, command, args...); err != nil {
		return errors.New("Tapd remove command failed")
	}
	// Deliberately remove only the execution declaration. The proof data and
	// PostgreSQL database are the user's assets and remain untouched.
	return removeFixedTree(manager.Paths.SnapshotRoot, filepath.Dir(manager.Paths.SnapshotRoot))
}

func (manager *NativeTapdManager) CLI(ctx context.Context, request appmanifest.TapdCLIRequest) (string, error) {
	args, err := appmanifest.TapdCLIArgs(request)
	if err != nil {
		return "", err
	}
	containerID, err := manager.runningContainerID(ctx)
	if err != nil || containerID == "" {
		return "", errors.New("Tapd container is not running")
	}
	execArgs := []string{"exec", containerID, "tapcli", "--network=" + appmanifest.TapdNetwork}
	execArgs = append(execArgs, args...)
	output, err := manager.Runner.Run(ctx, dockerPath, execArgs...)
	if err != nil {
		return output, errors.New("Tapd command failed")
	}
	return output, nil
}

func validateTapdTLSCertificate(raw []byte) error {
	if len(raw) == 0 || len(raw) > 64*1024 {
		return errors.New("Tapd LND certificate is invalid")
	}
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return errors.New("Tapd LND certificate is invalid")
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return errors.New("Tapd LND certificate is invalid")
	}
	return nil
}
