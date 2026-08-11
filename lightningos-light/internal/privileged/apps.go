package privileged

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"lightningos-light/internal/appmanifest"
)

const (
	defaultAppsRoot           = "/var/lib/lightningos/apps"
	defaultPrivilegedAppsRoot = "/var/lib/lightningos-privileged/apps"
	dockerPath                = "/usr/bin/docker"
	dockerComposePath         = "/usr/bin/docker-compose"
	ufwPath                   = "/usr/sbin/ufw"
)

type ComposeAppManager struct {
	Runner             CommandRunner
	AppsRoot           string
	PrivilegedAppsRoot string
	TempRoot           string
}

type composeAppSnapshot struct {
	root        string
	composePath string
	envPath     string
}

type roboSatsValidatedFiles struct {
	composeRaw     []byte
	caddyfileRaw   []byte
	certificateRaw []byte
	privateKeyRaw  []byte
}

var dockerCPUPercentPattern = regexp.MustCompile(`^([0-9]+(?:[.,][0-9]+)?)[[:space:]]*%$`)

func NewComposeAppManager(runner CommandRunner) *ComposeAppManager {
	return &ComposeAppManager{Runner: runner, AppsRoot: defaultAppsRoot, PrivilegedAppsRoot: defaultPrivilegedAppsRoot}
}

func (manager *ComposeAppManager) EnsureFirewallAccess(ctx context.Context, appID string, dryRun bool) (AppFirewallState, error) {
	var state AppFirewallState
	if manager == nil || manager.Runner == nil {
		return state, errors.New("compose app manager is unavailable")
	}
	port, err := appmanifest.CatalogExternalTCPPort(appID)
	if err != nil {
		return state, err
	}
	if dryRun {
		return AppFirewallState{Status: "validated"}, nil
	}
	statusOut, err := manager.Runner.Run(ctx, ufwPath, "status")
	if err != nil {
		return AppFirewallState{Status: "unavailable"}, nil
	}
	if !strings.Contains(strings.ToLower(statusOut), "status: active") {
		return AppFirewallState{Status: "inactive"}, nil
	}
	if _, err := manager.Runner.Run(ctx, ufwPath, "allow", strconv.Itoa(port)+"/tcp"); err != nil {
		return state, err
	}
	return AppFirewallState{Status: "active"}, nil
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

func (manager *ComposeAppManager) PrepareImage(ctx context.Context, appID string, variant appmanifest.AppImageVariant, dryRun bool) (AppImageState, error) {
	var state AppImageState
	image, unit, refresh, err := validatedCatalogImage(appID, variant)
	if err != nil {
		return state, err
	}
	if manager == nil || manager.Runner == nil {
		return state, errors.New("compose app manager is unavailable")
	}
	if dryRun {
		return AppImageState{Status: "validated"}, nil
	}
	if appID == appmanifest.BitcoinCoreID {
		return manager.prepareBitcoinCoreImage(ctx, unit)
	}
	if refresh {
		state, err = manager.refreshImageStatus(ctx, image, unit)
		if err != nil || state.Status == "preparing" {
			return state, err
		}
		if state.Status == "failed" {
			return state, errors.New("app image preparation previously failed")
		}
		return manager.scheduleImagePull(ctx, image, unit)
	}
	state, err = manager.imageStatus(ctx, image, unit)
	if err != nil || state.Status == "ready" || state.Status == "preparing" {
		return state, err
	}
	if state.Status == "failed" {
		return state, errors.New("app image preparation previously failed")
	}
	return manager.scheduleImagePull(ctx, image, unit)
}

func (manager *ComposeAppManager) scheduleImagePull(ctx context.Context, image string, unit string) (AppImageState, error) {
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
		return AppImageState{}, errors.New("app image preparation could not be scheduled")
	}
	return AppImageState{Status: "preparing"}, nil
}

func (manager *ComposeAppManager) ImageStatus(ctx context.Context, appID string, variant appmanifest.AppImageVariant) (AppImageState, error) {
	var state AppImageState
	image, unit, refresh, err := validatedCatalogImage(appID, variant)
	if err != nil {
		return state, err
	}
	if manager == nil || manager.Runner == nil {
		return state, errors.New("compose app manager is unavailable")
	}
	if appID == appmanifest.BitcoinCoreID {
		artifact, artifactErr := appmanifest.BitcoinCoreArtifactForGOARCH(runtime.GOARCH)
		if artifactErr != nil {
			return state, artifactErr
		}
		return manager.bitcoinCoreImageStatus(ctx, artifact, unit)
	}
	if refresh {
		return manager.refreshImageStatus(ctx, image, unit)
	}
	return manager.imageStatus(ctx, image, unit)
}

func (manager *ComposeAppManager) ProbeImage(ctx context.Context, appID string, variant appmanifest.AppImageVariant, dryRun bool) (AppImageProbe, error) {
	var probe AppImageProbe
	if appID != appmanifest.CPUMinerID {
		return probe, errors.New("app image probe is not allowed")
	}
	image, _, _, err := validatedCatalogImage(appID, variant)
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

// refreshImageStatus checks the transient pull unit before the local cache.
// Refresh-on-request catalog images must not report ready merely because an
// older local image with the same closed release tag is already present.
func (manager *ComposeAppManager) refreshImageStatus(ctx context.Context, image string, unit string) (AppImageState, error) {
	output, showErr := manager.Runner.Run(ctx, systemctlPath, "show",
		"--property=LoadState", "--property=ActiveState", "--property=SubState", "--property=Result", "--no-pager", unit)
	values := parseSystemdProperties(output)
	switch values["ActiveState"] {
	case "active", "activating", "reloading":
		return AppImageState{Status: "preparing"}, nil
	case "failed":
		return AppImageState{Status: "failed"}, nil
	case "inactive", "deactivating":
		if result := values["Result"]; result != "" && result != "success" {
			return AppImageState{Status: "failed"}, nil
		}
	}
	if showErr != nil && values["LoadState"] != "" && values["LoadState"] != "not-found" {
		return AppImageState{}, errors.New("app image preparation state is invalid")
	}
	if _, err := manager.Runner.Run(ctx, dockerPath, "image", "inspect", image); err == nil {
		return AppImageState{Status: "ready"}, nil
	}
	return AppImageState{Status: "absent"}, nil
}

func validatedCatalogImage(appID string, variant appmanifest.AppImageVariant) (string, string, bool, error) {
	image, err := appmanifest.CatalogImageForVariant(appID, variant)
	if err != nil {
		return "", "", false, err
	}
	refresh, err := appmanifest.CatalogImageRequiresRefresh(appID, variant)
	if err != nil {
		return "", "", false, err
	}
	units := map[string]map[appmanifest.AppImageVariant]string{
		appmanifest.CPUMinerID: {
			appmanifest.CPUMinerImageBaseline:   "lightningos-cpuminer-image-baseline",
			appmanifest.CPUMinerImageFastPinned: "lightningos-cpuminer-image-fast-pinned",
			appmanifest.CPUMinerImageFastLatest: "lightningos-cpuminer-image-fast-latest",
		},
		appmanifest.RoboSatsID: {
			appmanifest.RoboSatsImageClient: "lightningos-robosats-image-client",
			appmanifest.RoboSatsImageTor:    "lightningos-robosats-image-tor",
			appmanifest.RoboSatsImageProxy:  "lightningos-robosats-image-proxy",
		},
		appmanifest.BitcoinCoreID: {
			appmanifest.BitcoinCoreImageNode: "lightningos-bitcoincore-image-node",
		},
		appmanifest.BTCPayID: {
			appmanifest.BTCPayImageServer:    "lightningos-btcpay-image-server",
			appmanifest.BTCPayImageNbxplorer: "lightningos-btcpay-image-nbxplorer",
			appmanifest.BTCPayImagePostgres:  "lightningos-btcpay-image-postgres",
			appmanifest.BTCPayImageTor:       "lightningos-btcpay-image-tor",
		},
	}
	appUnits, ok := units[appID]
	if !ok {
		return "", "", false, errors.New("app image manifest is not allowed")
	}
	unit, ok := appUnits[variant]
	if !ok {
		return "", "", false, errors.New("app image variant is not allowed")
	}
	return image, unit, refresh, nil
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
	manifest, err := appmanifest.ComposeManifestForApp(appID)
	if err != nil {
		return errors.New("app manifest is not allowed")
	}
	if action != AppLifecycleStart && action != AppLifecycleStop && action != AppLifecycleRestart {
		return errors.New("app lifecycle action is not allowed")
	}
	if action == AppLifecycleRestart && manifest.ID != appmanifest.BitcoinCoreID {
		return errors.New("app lifecycle action is not allowed")
	}

	var snapshot composeAppSnapshot
	var cleanup func()
	var images []string
	switch manifest.ID {
	case appmanifest.CPUMinerID:
		composeRaw, envRaw, err := manager.validatedCPUMinerFiles()
		if err != nil {
			return err
		}
		image, err := appmanifest.CPUMinerImage(envRaw)
		if err != nil {
			return errors.New("app image selection is invalid")
		}
		images = []string{image}
		if dryRun {
			return nil
		}
		snapshot, cleanup, err = manager.createSnapshot(manifest, composeRaw, envRaw)
		if err != nil {
			return err
		}
	case appmanifest.RoboSatsID:
		files, err := manager.validatedRoboSatsFiles()
		if err != nil {
			return err
		}
		images = appmanifest.RoboSatsImages()
		if dryRun {
			return nil
		}
		snapshot, cleanup, err = manager.createRoboSatsSnapshot(manifest, files)
		if err != nil {
			return err
		}
	case appmanifest.BitcoinCoreID:
		requireConfig := action == AppLifecycleStart || action == AppLifecycleRestart
		if requireConfig {
			if err := manager.validateBitcoinCoreLifecycleAttestation(); err != nil {
				return err
			}
		}
		snapshot, cleanup, err = manager.createBitcoinCoreExecutionSnapshot(ctx, dryRun, requireConfig)
		if err != nil {
			return err
		}
		if requireConfig {
			if dryRun {
				return nil
			}
			state, err := manager.ImageStatus(ctx, appmanifest.BitcoinCoreID, appmanifest.BitcoinCoreImageNode)
			if err != nil || state.Status != "ready" {
				return errors.New("verified bitcoin core image is not ready")
			}
		}
		if dryRun {
			return nil
		}
	default:
		return errors.New("app manifest is not allowed")
	}
	defer cleanup()

	if action == AppLifecycleStart {
		for _, image := range images {
			if _, err := manager.Runner.Run(ctx, dockerPath, "image", "inspect", image); err != nil {
				return errors.New("app image is not available locally")
			}
		}
	}

	commandPath, prefix, err := manager.resolveCompose(ctx)
	if err != nil {
		return err
	}
	args := append([]string(nil), prefix...)
	if snapshot.envPath != "" {
		args = append(args, "--env-file", snapshot.envPath)
	}
	args = append(args,
		"--project-name", manifest.Project,
		"--project-directory", snapshot.root,
		"-f", snapshot.composePath,
	)
	switch action {
	case AppLifecycleStart:
		args = append(args, "up", "-d")
	case AppLifecycleStop:
		args = append(args, "stop", "--timeout", strconv.Itoa(manifest.StopTimeoutSeconds))
	case AppLifecycleRestart:
		args = append(args, "restart", "--timeout", strconv.Itoa(manifest.StopTimeoutSeconds), manifest.PrimaryService)
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
	manifest, err := appmanifest.ComposeManifestForApp(appID)
	if err != nil {
		return errors.New("app manifest is not allowed")
	}

	var snapshot composeAppSnapshot
	var cleanup func()
	removePersistentSnapshot := false
	switch appID {
	case appmanifest.CPUMinerID:
		composeRaw, envRaw, err := manager.validatedCPUMinerFiles()
		if err != nil {
			return err
		}
		if dryRun {
			return nil
		}
		snapshot, cleanup, err = manager.createSnapshot(manifest, composeRaw, envRaw)
		if err != nil {
			return err
		}
	case appmanifest.RoboSatsID:
		files, err := manager.validatedRoboSatsFiles()
		if err != nil {
			return err
		}
		if dryRun {
			return nil
		}
		snapshot, cleanup, err = manager.createRoboSatsSnapshot(manifest, files)
		if err != nil {
			return err
		}
		removePersistentSnapshot = true
	case appmanifest.BitcoinCoreID:
		snapshot, cleanup, err = manager.createBitcoinCoreExecutionSnapshot(ctx, dryRun, false)
		if err != nil {
			return err
		}
		if dryRun {
			return nil
		}
		removePersistentSnapshot = true
	default:
		return errors.New("app manifest is not allowed")
	}
	defer cleanup()

	commandPath, prefix, err := manager.resolveCompose(ctx)
	if err != nil {
		return err
	}
	args := append([]string(nil), prefix...)
	if snapshot.envPath != "" {
		args = append(args, "--env-file", snapshot.envPath)
	}
	args = append(args,
		"--project-name", manifest.Project,
		"--project-directory", snapshot.root,
		"-f", snapshot.composePath,
		"down", "--remove-orphans", "--timeout", strconv.Itoa(manifest.StopTimeoutSeconds),
	)
	if _, err := manager.Runner.Run(ctx, commandPath, args...); err != nil {
		return errors.New("app remove command failed")
	}
	if removePersistentSnapshot && appID == appmanifest.RoboSatsID {
		if err := manager.removeRoboSatsExecutionSnapshot(snapshot.root); err != nil {
			return errors.New("failed to remove app execution snapshot")
		}
	} else if removePersistentSnapshot && appID == appmanifest.BitcoinCoreID {
		if err := manager.removeBitcoinCoreExecutionSnapshot(snapshot.root); err != nil {
			return errors.New("failed to remove app execution snapshot")
		}
	}
	return nil
}

func (manager *ComposeAppManager) removeRoboSatsExecutionSnapshot(snapshotRoot string) error {
	privilegedAppsRoot := manager.PrivilegedAppsRoot
	if privilegedAppsRoot == "" {
		privilegedAppsRoot = defaultPrivilegedAppsRoot
	}
	expectedRoot := filepath.Join(filepath.Clean(privilegedAppsRoot), appmanifest.RoboSatsID)
	if filepath.Clean(snapshotRoot) != expectedRoot {
		return errors.New("invalid app execution snapshot")
	}
	if err := validateRegularDirectory(expectedRoot); err != nil {
		return errors.New("invalid app execution snapshot")
	}
	return os.RemoveAll(expectedRoot)
}

func (manager *ComposeAppManager) Inspect(ctx context.Context, appID string) (AppInspection, error) {
	var inspection AppInspection
	if manager == nil || manager.Runner == nil {
		return inspection, errors.New("compose app manager is unavailable")
	}
	manifest, err := appmanifest.ComposeManifestForApp(appID)
	if err != nil {
		return inspection, errors.New("app manifest is not allowed")
	}

	var snapshot composeAppSnapshot
	var cleanup func()
	switch manifest.ID {
	case appmanifest.CPUMinerID:
		composeRaw, envRaw, err := manager.validatedCPUMinerFiles()
		if err != nil {
			return inspection, err
		}
		snapshot, cleanup, err = manager.createSnapshot(manifest, composeRaw, envRaw)
		if err != nil {
			return inspection, err
		}
	case appmanifest.RoboSatsID:
		files, err := manager.validatedRoboSatsFiles()
		if err != nil {
			return inspection, err
		}
		snapshot, cleanup, err = manager.createRoboSatsSnapshot(manifest, files)
		if err != nil {
			return inspection, err
		}
	case appmanifest.BitcoinCoreID:
		snapshot, cleanup, err = manager.createBitcoinCoreInspectionSnapshot(ctx)
		if err != nil {
			return inspection, err
		}
	default:
		return inspection, errors.New("app manifest is not allowed")
	}
	defer cleanup()

	commandPath, prefix, err := manager.resolveCompose(ctx)
	if err != nil {
		return inspection, err
	}
	baseArgs := append([]string(nil), prefix...)
	if snapshot.envPath != "" {
		baseArgs = append(baseArgs, "--env-file", snapshot.envPath)
	}
	baseArgs = append(baseArgs,
		"--project-name", manifest.Project,
		"--project-directory", snapshot.root,
		"-f", snapshot.composePath,
	)
	output, err := manager.Runner.Run(ctx, commandPath, append(append([]string(nil), baseArgs...), "ps", "--services", "--filter", "status=running")...)
	if err != nil {
		return inspection, errors.New("app status command failed")
	}
	inspection.Status = "stopped"
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == manifest.PrimaryService {
			inspection.Status = "running"
			break
		}
	}
	if inspection.Status != "running" {
		return inspection, nil
	}
	if manifest.ID != appmanifest.CPUMinerID {
		return inspection, nil
	}

	output, err = manager.Runner.Run(ctx, commandPath, append(append([]string(nil), baseArgs...), "ps", "-q", manifest.PrimaryService)...)
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

func (manager *ComposeAppManager) validatedRoboSatsFiles() (roboSatsValidatedFiles, error) {
	var files roboSatsValidatedFiles
	appsRoot := manager.AppsRoot
	if appsRoot == "" {
		appsRoot = defaultAppsRoot
	}
	appRoot := filepath.Join(appsRoot, appmanifest.RoboSatsID)
	tlsDir := filepath.Join(appRoot, appmanifest.RoboSatsTLSDir)
	if err := validateRegularDirectory(appRoot); err != nil {
		return files, errors.New("app directory is unavailable")
	}
	fail := func(message string) (roboSatsValidatedFiles, error) {
		return roboSatsValidatedFiles{}, errors.New(message)
	}
	if err := validateRegularDirectory(tlsDir); err != nil {
		return fail("app TLS directory is unavailable")
	}

	composePath := filepath.Join(appRoot, appmanifest.RoboSatsComposeFile)
	caddyfilePath := filepath.Join(appRoot, appmanifest.RoboSatsCaddyfileFile)
	composeRaw, err := readRegularFile(composePath, 64*1024)
	if err != nil {
		return fail("app compose manifest is unavailable")
	}
	expectedCompose := appmanifest.RoboSatsCompose(caddyfilePath, tlsDir)
	if !bytes.Equal(composeRaw, []byte(expectedCompose)) {
		return fail("app compose manifest does not match the catalog")
	}
	caddyfileRaw, err := readRegularFile(caddyfilePath, 16*1024)
	if err != nil {
		return fail("app proxy configuration is unavailable")
	}
	if !bytes.Equal(caddyfileRaw, []byte(appmanifest.RoboSatsCaddyfile())) {
		return fail("app proxy configuration does not match the catalog")
	}
	certificateRaw, err := readRegularFile(filepath.Join(tlsDir, "server.crt"), 64*1024)
	if err != nil {
		return fail("app TLS certificate is unavailable")
	}
	privateKeyRaw, err := readRegularFile(filepath.Join(tlsDir, "server.key"), 64*1024)
	if err != nil {
		return fail("app TLS private key is unavailable")
	}
	if err := validateTLSKeyPair(certificateRaw, privateKeyRaw); err != nil {
		return fail("app TLS key pair is invalid")
	}
	files.composeRaw = composeRaw
	files.caddyfileRaw = caddyfileRaw
	files.certificateRaw = certificateRaw
	files.privateKeyRaw = privateKeyRaw
	return files, nil
}

func (manager *ComposeAppManager) createSnapshot(manifest appmanifest.ComposeManifest, composeRaw []byte, envRaw []byte) (composeAppSnapshot, func(), error) {
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
		composePath: filepath.Join(snapshotRoot, manifest.ComposeFile),
	}
	if manifest.EnvFile != "" {
		snapshot.envPath = filepath.Join(snapshotRoot, manifest.EnvFile)
	}
	if err := os.WriteFile(snapshot.composePath, composeRaw, 0600); err != nil {
		cleanup()
		return composeAppSnapshot{}, func() {}, errors.New("failed to snapshot app compose manifest")
	}
	if snapshot.envPath != "" {
		if err := os.WriteFile(snapshot.envPath, envRaw, 0600); err != nil {
			cleanup()
			return composeAppSnapshot{}, func() {}, errors.New("failed to snapshot app environment")
		}
	}
	return snapshot, cleanup, nil
}

func (manager *ComposeAppManager) createRoboSatsSnapshot(manifest appmanifest.ComposeManifest, files roboSatsValidatedFiles) (composeAppSnapshot, func(), error) {
	var snapshot composeAppSnapshot
	privilegedAppsRoot := manager.PrivilegedAppsRoot
	if privilegedAppsRoot == "" {
		privilegedAppsRoot = defaultPrivilegedAppsRoot
	}
	if err := ensureDirectoryTreeNoSymlink(privilegedAppsRoot, 0700); err != nil {
		return snapshot, func() {}, errors.New("failed to secure privileged app root")
	}
	snapshotRoot := filepath.Join(privilegedAppsRoot, manifest.ID)
	if err := ensureDirectoryTreeNoSymlink(snapshotRoot, 0700); err != nil {
		return snapshot, func() {}, errors.New("failed to secure app execution snapshot")
	}
	cleanup := func() {}
	tlsDir := filepath.Join(snapshotRoot, appmanifest.RoboSatsTLSDir)
	if err := ensureDirectoryTreeNoSymlink(tlsDir, 0700); err != nil {
		return snapshot, func() {}, errors.New("failed to create app TLS snapshot")
	}
	caddyfilePath := filepath.Join(snapshotRoot, appmanifest.RoboSatsCaddyfileFile)
	certificatePath := filepath.Join(tlsDir, "server.crt")
	privateKeyPath := filepath.Join(tlsDir, "server.key")
	for _, file := range []struct {
		path string
		data []byte
		err  string
	}{
		{caddyfilePath, files.caddyfileRaw, "failed to snapshot app proxy configuration"},
		{certificatePath, files.certificateRaw, "failed to snapshot app TLS certificate"},
		{privateKeyPath, files.privateKeyRaw, "failed to snapshot app TLS private key"},
	} {
		if err := writeAtomicRegularFile(file.path, file.data, 0600); err != nil {
			return composeAppSnapshot{}, func() {}, errors.New(file.err)
		}
	}
	executionCompose := []byte(appmanifest.RoboSatsCompose(caddyfilePath, tlsDir))
	snapshot = composeAppSnapshot{
		root:        snapshotRoot,
		composePath: filepath.Join(snapshotRoot, manifest.ComposeFile),
	}
	if err := writeAtomicRegularFile(snapshot.composePath, executionCompose, 0600); err != nil {
		return composeAppSnapshot{}, func() {}, errors.New("failed to snapshot app compose manifest")
	}
	return snapshot, cleanup, nil
}

func validateRegularDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid directory")
	}
	return nil
}

func ensureDirectoryTreeNoSymlink(path string, mode os.FileMode) error {
	cleanPath := filepath.Clean(path)
	if err := os.MkdirAll(cleanPath, mode); err != nil {
		return err
	}
	for current := cleanPath; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("directory tree contains an invalid component")
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return os.Chmod(cleanPath, mode)
}

func writeAtomicRegularFile(path string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".lightningos-write-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return nil
}

func validateTLSKeyPair(certificateRaw []byte, privateKeyRaw []byte) error {
	certificateBlock, certificateRest := pem.Decode(certificateRaw)
	if certificateBlock == nil || certificateBlock.Type != "CERTIFICATE" || len(bytes.TrimSpace(certificateRest)) != 0 {
		return errors.New("invalid certificate PEM")
	}
	certificate, err := x509.ParseCertificate(certificateBlock.Bytes)
	if err != nil {
		return err
	}
	certificateKey, ok := certificate.PublicKey.(*rsa.PublicKey)
	if !ok {
		return errors.New("certificate key is not RSA")
	}

	privateKeyBlock, privateKeyRest := pem.Decode(privateKeyRaw)
	if privateKeyBlock == nil || len(bytes.TrimSpace(privateKeyRest)) != 0 {
		return errors.New("invalid private key PEM")
	}
	var privateKey *rsa.PrivateKey
	switch privateKeyBlock.Type {
	case "RSA PRIVATE KEY":
		privateKey, err = x509.ParsePKCS1PrivateKey(privateKeyBlock.Bytes)
	case "PRIVATE KEY":
		var parsed any
		parsed, err = x509.ParsePKCS8PrivateKey(privateKeyBlock.Bytes)
		if err == nil {
			var ok bool
			privateKey, ok = parsed.(*rsa.PrivateKey)
			if !ok {
				return errors.New("private key is not RSA")
			}
		}
	default:
		return errors.New("unsupported private key PEM")
	}
	if err != nil {
		return err
	}
	if err := privateKey.Validate(); err != nil {
		return err
	}
	if certificateKey.E != privateKey.PublicKey.E || certificateKey.N.Cmp(privateKey.PublicKey.N) != 0 {
		return errors.New("certificate and private key do not match")
	}
	return nil
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
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return nil, errors.New("file changed before reading")
	}
	data := make([]byte, info.Size())
	if _, err := io.ReadFull(file, data); err != nil {
		return nil, err
	}
	finalInfo, err := file.Stat()
	if err != nil || !os.SameFile(openedInfo, finalInfo) || finalInfo.Size() != info.Size() {
		return nil, errors.New("file changed while reading")
	}
	return data, nil
}
