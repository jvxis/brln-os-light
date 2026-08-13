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
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"
)

const (
	defaultAppsRoot           = "/var/lib/lightningos/apps"
	defaultAppsDataRoot       = "/var/lib/lightningos/apps-data"
	defaultPrivilegedAppsRoot = "/var/lib/lightningos-privileged/apps"
	defaultLNDDataRoot        = "/data/lnd"
	dockerPath                = "/usr/bin/docker"
	dockerComposePath         = "/usr/bin/docker-compose"
	ufwPath                   = "/usr/sbin/ufw"
	btcpayPostgresReadyTries  = 30
	btcpayPostgresRetryDelay  = time.Second
)

type ComposeAppManager struct {
	Runner             CommandRunner
	AppsRoot           string
	AppsDataRoot       string
	PrivilegedAppsRoot string
	LNDDataRoot        string
	LNDConfigPath      string
	TempRoot           string
	ElectrsRPCProbe    electrsRPCProbeFunc
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

type btcpayValidatedFiles struct {
	composeRaw         []byte
	envRaw             []byte
	dbInitRaw          []byte
	certificateRaw     []byte
	macaroonRaw        []byte
	joinBitcoinNetwork bool
	useTorProxy        bool
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
	if appID == appmanifest.LNDgID && variant == appmanifest.LNDgImageApp {
		return manager.prepareLNDgImage(ctx, unit)
	}
	if appID == appmanifest.ElectrsID {
		return manager.prepareElectrsImage(ctx, unit)
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
	if appID == appmanifest.LNDgID && variant == appmanifest.LNDgImageApp {
		return manager.lndgImageStatus(ctx, unit)
	}
	if appID == appmanifest.ElectrsID {
		return manager.electrsImageStatus(ctx, unit)
	}
	if refresh {
		return manager.refreshImageStatus(ctx, image, unit)
	}
	return manager.imageStatus(ctx, image, unit)
}

func (manager *ComposeAppManager) ProbeImage(ctx context.Context, appID string, variant appmanifest.AppImageVariant, dryRun bool) (AppImageProbe, error) {
	var probe AppImageProbe
	if appID != appmanifest.CPUMinerID && appID != appmanifest.TapdID && appID != appmanifest.PublicPoolID && appID != appmanifest.BarkWalletID {
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
	if appID == appmanifest.TapdID {
		// The v0.8.0 verifier bundled by upstream has a case mismatch in one
		// signer name. Do not weaken its five-signature quorum here. The image
		// is already selected by its immutable official manifest digest; probe
		// both shipped binaries under a closed runtime and require the exact
		// version strings from that release.
		baseArgs := []string{"run", "--rm", "--pull", "never", "--network", "none",
			"--read-only", "--user", "65534:65534", "--cap-drop", "ALL",
			"--security-opt", "no-new-privileges"}
		tapdArgs := append(append([]string{}, baseArgs...), "--entrypoint", "/bin/tapd", image, "--version")
		tapdOutput, tapdErr := manager.Runner.Run(ctx, dockerPath, tapdArgs...)
		tapcliArgs := append(append([]string{}, baseArgs...), "--entrypoint", "/bin/tapcli", image, "--version")
		tapcliOutput, tapcliErr := manager.Runner.Run(ctx, dockerPath, tapcliArgs...)
		probe.Runnable = tapdErr == nil && tapcliErr == nil &&
			strings.TrimSpace(tapdOutput) == appmanifest.TapdDaemonVersionOutput &&
			strings.TrimSpace(tapcliOutput) == appmanifest.TapdCLIVersionOutput
		return probe, nil
	}
	if appID == appmanifest.PublicPoolID {
		baseArgs := []string{"run", "--rm", "--pull", "never", "--network", "none", "--read-only",
			"--user", "65532:65532", "--cap-drop", "ALL", "--security-opt", "no-new-privileges"}
		switch variant {
		case appmanifest.PublicPoolImageBackend:
			args := append(append([]string{}, baseArgs...), "--entrypoint", "/usr/local/bin/node",
				image, "--version")
			output, runErr := manager.Runner.Run(ctx, dockerPath, args...)
			probe.Runnable = runErr == nil && strings.TrimSpace(output) == appmanifest.PublicPoolBackendVersionOutput
		case appmanifest.PublicPoolImageUI:
			args := append(append([]string{}, baseArgs...), "--tmpfs",
				"/run/lightningos-bin:rw,exec,nosuid,nodev,size=64m,uid=65532,gid=65532,mode=0700",
				"--entrypoint", "/bin/sh", image, "-c",
				"cp /usr/bin/caddy /run/lightningos-bin/caddy && exec /run/lightningos-bin/caddy version")
			output, runErr := manager.Runner.Run(ctx, dockerPath, args...)
			probe.Runnable = runErr == nil && strings.TrimSpace(output) == appmanifest.PublicPoolUIVersionOutput
		}
		return probe, nil
	}
	if appID == appmanifest.BarkWalletID {
		baseArgs := []string{"run", "--rm", "--pull", "never", "--network", "none", "--read-only",
			"--cap-drop", "ALL", "--security-opt", "no-new-privileges"}
		var args []string
		switch variant {
		case appmanifest.BarkWalletImageWeb:
			args = append(append([]string{}, baseArgs...), "--user", "101:101", "--entrypoint", "/usr/sbin/nginx", image, "-v")
		case appmanifest.BarkWalletImageAPI:
			args = append(append([]string{}, baseArgs...), "--user", "65530:65530", "--entrypoint", "/usr/local/bin/node", image, "--version")
		case appmanifest.BarkWalletImageDaemon:
			checksum, checksumErr := barkWalletDaemonChecksum()
			if checksumErr != nil {
				return probe, checksumErr
			}
			checksumArgs := append(append([]string{}, baseArgs...), "--user", "65531:65531", "--entrypoint", "/usr/bin/sha256sum", image, "/usr/local/bin/barkd")
			checksumOutput, checksumRunErr := manager.Runner.Run(ctx, dockerPath, checksumArgs...)
			if checksumRunErr != nil || strings.TrimSpace(checksumOutput) != checksum+"  /usr/local/bin/barkd" {
				return probe, nil
			}
			args = append(append([]string{}, baseArgs...), "--user", "65531:65531", "--entrypoint", "/usr/local/bin/barkd", image, "--version")
		case appmanifest.BarkWalletImageProxy:
			args = append(append([]string{}, baseArgs...), "--user", "65532:65532", "--tmpfs",
				"/run/lightningos-bin:rw,exec,nosuid,nodev,size=64m,uid=65532,gid=65532,mode=0700",
				"--entrypoint", "/bin/sh", image, "-c",
				"cp /usr/bin/caddy /run/lightningos-bin/caddy && exec /run/lightningos-bin/caddy version")
		}
		output, runErr := manager.Runner.Run(ctx, dockerPath, args...)
		probe.Runnable = runErr == nil && strings.TrimSpace(output) != ""
		if variant == appmanifest.BarkWalletImageDaemon {
			probe.Runnable = probe.Runnable && strings.TrimSpace(output) == appmanifest.BarkWalletDaemonVersionOutput
		}
		return probe, nil
	}

	args := []string{"run", "--rm", image, "cpuminer", "--algo", "sha256d", "--benchmark", "--time-limit", "2"}
	_, err = manager.Runner.Run(ctx, dockerPath, args...)
	probe.Runnable = err == nil
	return probe, nil
}

func barkWalletDaemonChecksum() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return appmanifest.BarkWalletDaemonAMD64BinarySHA256, nil
	case "arm64":
		return appmanifest.BarkWalletDaemonARM64BinarySHA256, nil
	default:
		return "", errors.New("Bark Wallet daemon architecture is not allowed")
	}
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
		appmanifest.LNDgID: {
			appmanifest.LNDgImageApp:      "lightningos-lndg-image-app",
			appmanifest.LNDgImagePostgres: "lightningos-lndg-image-postgres",
		},
		appmanifest.LNbitsID: {
			appmanifest.LNbitsImageApp: "lightningos-lnbits-image-app",
		},
		appmanifest.ElectrsID: {
			appmanifest.ElectrsImageApp: "lightningos-electrs-image-app",
		},
		appmanifest.TapdID: {
			appmanifest.TapdImageApp: "lightningos-tapd-image-app",
		},
		appmanifest.PublicPoolID: {
			appmanifest.PublicPoolImageBackend: "lightningos-publicpool-image-backend",
			appmanifest.PublicPoolImageUI:      "lightningos-publicpool-image-ui",
		},
		appmanifest.BarkWalletID: {
			appmanifest.BarkWalletImageWeb:    "lightningos-bark-image-web",
			appmanifest.BarkWalletImageAPI:    "lightningos-bark-image-api",
			appmanifest.BarkWalletImageDaemon: "lightningos-bark-image-daemon",
			appmanifest.BarkWalletImageProxy:  "lightningos-bark-image-proxy",
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
	case appmanifest.BTCPayID:
		files, err := manager.validatedBTCPayFiles()
		if err != nil {
			return err
		}
		images = appmanifest.BTCPayImages(files.useTorProxy)
		if dryRun {
			return nil
		}
		snapshot, cleanup, err = manager.createBTCPaySnapshot(files)
		if err != nil {
			return err
		}
	case appmanifest.LNDgID:
		files, err := manager.validatedLNDgFiles()
		if err != nil {
			return err
		}
		images = []string{appmanifest.LNDgImage, appmanifest.LNDgPostgresImage}
		if action == AppLifecycleStart && !dryRun {
			state, imageErr := manager.lndgImageStatus(ctx, "lightningos-lndg-image-app")
			if imageErr != nil || state.Status != "ready" {
				return errors.New("verified LNDg image is not ready")
			}
		}
		if dryRun {
			return nil
		}
		snapshot, cleanup, err = manager.createLNDgSnapshot(files)
		if err != nil {
			return err
		}
	case appmanifest.LNbitsID:
		files, err := manager.validatedLNbitsFiles()
		if err != nil {
			return err
		}
		images = []string{appmanifest.LNbitsImage}
		if dryRun {
			return nil
		}
		snapshot, cleanup, err = manager.createLNbitsSnapshot(files)
		if err != nil {
			return err
		}
	case appmanifest.ElectrsID:
		files, err := manager.validatedElectrsFiles()
		if err != nil {
			return err
		}
		if action == AppLifecycleStart {
			if dryRun {
				return nil
			}
			if err := manager.validateElectrsBitcoin(ctx, files.runtime, files.cookieRaw); err != nil {
				return err
			}
			state, err := manager.electrsImageStatus(ctx, "lightningos-electrs-image-app")
			if err != nil || state.Status != "ready" {
				return errors.New("verified Electrs image is not ready")
			}
		}
		if dryRun {
			return nil
		}
		snapshot, cleanup, err = manager.createElectrsSnapshot(files)
		if err != nil {
			return err
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
		if manifest.ID == appmanifest.BTCPayID {
			databaseArgs := append(append([]string(nil), args...), "up", "-d", "btcpay-db")
			if _, err := manager.Runner.Run(ctx, commandPath, databaseArgs...); err != nil {
				return errors.New("app database start command failed")
			}
			if err := manager.ensureBTCPayNbxplorerDatabase(ctx, commandPath, args); err != nil {
				return err
			}
		}
		if manifest.ID == appmanifest.LNDgID {
			appsDataRoot := manager.AppsDataRoot
			if appsDataRoot == "" {
				appsDataRoot = defaultAppsDataRoot
			}
			if err := prepareLNDgWritableData(filepath.Join(appsDataRoot, appmanifest.LNDgID, "data")); err != nil {
				return errors.New("LNDg writable data preparation failed")
			}
			databaseArgs := append(append([]string(nil), args...), "up", "-d", appmanifest.LNDgDatabaseService)
			if _, err := manager.Runner.Run(ctx, commandPath, databaseArgs...); err != nil {
				return errors.New("LNDg database start command failed")
			}
			if err := manager.waitAndSyncLNDgDatabase(ctx, commandPath, args); err != nil {
				return err
			}
			if err := manager.ensureLNDgHostAccess(ctx); err != nil {
				return err
			}
			if err := manager.refreshLNDgSnapshotCertificate(snapshot.root); err != nil {
				return err
			}
		}
		if manifest.ID == appmanifest.LNbitsID {
			appsDataRoot := manager.AppsDataRoot
			if appsDataRoot == "" {
				appsDataRoot = defaultAppsDataRoot
			}
			if err := prepareLNbitsWritableData(filepath.Join(appsDataRoot, appmanifest.LNbitsID, "data")); err != nil {
				return errors.New("LNbits writable data preparation failed")
			}
			if err := manager.migrateLNbitsLegacySettings(ctx); err != nil {
				return err
			}
			if err := manager.ensureLNbitsHostAccess(ctx); err != nil {
				return err
			}
			// A clean install does not have lnbits_default yet. Materialize the
			// reviewed container and its network without starting it, so the
			// broker can bind the internal LND REST firewall rule to the actual
			// Compose subnet before LNbits becomes reachable.
			createArgs := append(append([]string(nil), args...), "create")
			if _, err := manager.Runner.Run(ctx, commandPath, createArgs...); err != nil {
				return errors.New("LNbits container preparation failed")
			}
			if err := manager.ensureLNbitsInternalFirewall(ctx); err != nil {
				return err
			}
			if err := manager.refreshLNbitsSnapshotCertificate(snapshot.root); err != nil {
				return err
			}
		}
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
	case appmanifest.BTCPayID:
		files, err := manager.validatedBTCPayFiles()
		if err != nil {
			return err
		}
		if dryRun {
			return nil
		}
		snapshot, cleanup, err = manager.createBTCPaySnapshot(files)
		if err != nil {
			return err
		}
		removePersistentSnapshot = true
	case appmanifest.LNDgID:
		files, err := manager.validatedLNDgFiles()
		if err != nil {
			return err
		}
		if dryRun {
			return nil
		}
		snapshot, cleanup, err = manager.createLNDgSnapshot(files)
		if err != nil {
			return err
		}
		removePersistentSnapshot = true
	case appmanifest.LNbitsID:
		files, err := manager.validatedLNbitsFiles()
		if err != nil {
			return err
		}
		if dryRun {
			return nil
		}
		snapshot, cleanup, err = manager.createLNbitsSnapshot(files)
		if err != nil {
			return err
		}
		removePersistentSnapshot = true
	case appmanifest.ElectrsID:
		files, err := manager.validatedElectrsFiles()
		if err != nil {
			return err
		}
		if dryRun {
			return nil
		}
		snapshot, cleanup, err = manager.createElectrsSnapshot(files)
		if err != nil {
			return err
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
	if manifest.RemoveVolumes {
		args = append(args, "--volumes")
	}
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
	} else if removePersistentSnapshot && appID == appmanifest.BTCPayID {
		if err := manager.removeBTCPayExecutionSnapshot(snapshot.root); err != nil {
			return errors.New("failed to remove app execution snapshot")
		}
	} else if removePersistentSnapshot && appID == appmanifest.LNDgID {
		if err := manager.removeLNDgExecutionSnapshot(snapshot.root); err != nil {
			return errors.New("failed to remove app execution snapshot")
		}
	} else if removePersistentSnapshot && appID == appmanifest.LNbitsID {
		if err := manager.removeLNbitsExecutionSnapshot(snapshot.root); err != nil {
			return errors.New("failed to remove app execution snapshot")
		}
	} else if removePersistentSnapshot && appID == appmanifest.ElectrsID {
		if err := manager.removeElectrsExecutionSnapshot(snapshot.root); err != nil {
			return errors.New("failed to remove app execution snapshot")
		}
	}
	return nil
}

func (manager *ComposeAppManager) removeBTCPayExecutionSnapshot(snapshotRoot string) error {
	privilegedAppsRoot := manager.PrivilegedAppsRoot
	if privilegedAppsRoot == "" {
		privilegedAppsRoot = defaultPrivilegedAppsRoot
	}
	expectedRoot := filepath.Join(filepath.Clean(privilegedAppsRoot), appmanifest.BTCPayID)
	if filepath.Clean(snapshotRoot) != expectedRoot {
		return errors.New("invalid app execution snapshot")
	}
	if err := validateRegularDirectory(expectedRoot); err != nil {
		return errors.New("invalid app execution snapshot")
	}
	return os.RemoveAll(expectedRoot)
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
	case appmanifest.BTCPayID:
		files, err := manager.validatedBTCPayFiles()
		if err != nil {
			return inspection, err
		}
		snapshot, cleanup, err = manager.createBTCPaySnapshot(files)
		if err != nil {
			return inspection, err
		}
	case appmanifest.LNDgID:
		files, err := manager.validatedLNDgFiles()
		if err != nil {
			return inspection, err
		}
		snapshot, cleanup, err = manager.createLNDgSnapshot(files)
		if err != nil {
			return inspection, err
		}
	case appmanifest.LNbitsID:
		files, err := manager.validatedLNbitsFiles()
		if err != nil {
			return inspection, err
		}
		snapshot, cleanup, err = manager.createLNbitsSnapshot(files)
		if err != nil {
			return inspection, err
		}
	case appmanifest.ElectrsID:
		files, err := manager.validatedElectrsFiles()
		if err != nil {
			return inspection, err
		}
		snapshot, cleanup, err = manager.createElectrsSnapshot(files)
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

// validatedBTCPayFiles accepts only the manager's closed catalog output and
// proves that the LND material is the dedicated BTCPay credential rather than
// the native node's admin macaroon. Secret bytes are never included in errors.
func (manager *ComposeAppManager) validatedBTCPayFiles() (btcpayValidatedFiles, error) {
	var files btcpayValidatedFiles
	appsRoot := manager.AppsRoot
	if appsRoot == "" {
		appsRoot = defaultAppsRoot
	}
	appsDataRoot := manager.AppsDataRoot
	if appsDataRoot == "" {
		appsDataRoot = defaultAppsDataRoot
	}
	lndDataRoot := manager.LNDDataRoot
	if lndDataRoot == "" {
		lndDataRoot = defaultLNDDataRoot
	}
	appRoot := filepath.Join(appsRoot, appmanifest.BTCPayID)
	dataRoot := filepath.Join(appsDataRoot, appmanifest.BTCPayID)
	lndDir := filepath.Join(dataRoot, appmanifest.BTCPayLNDDir)
	for _, directory := range []string{
		appsRoot,
		appRoot,
		appsDataRoot,
		dataRoot,
		filepath.Join(dataRoot, "data"),
		filepath.Join(dataRoot, "nbxplorer"),
		filepath.Join(dataRoot, "pgdata"),
		lndDir,
		lndDataRoot,
		filepath.Join(lndDataRoot, "data"),
		filepath.Join(lndDataRoot, "data", "chain"),
		filepath.Join(lndDataRoot, "data", "chain", "bitcoin"),
		filepath.Join(lndDataRoot, "data", "chain", "bitcoin", "mainnet"),
	} {
		if err := validateRegularDirectory(directory); err != nil {
			return files, errors.New("BTCPay directory is unavailable")
		}
	}
	fail := func(message string) (btcpayValidatedFiles, error) {
		return btcpayValidatedFiles{}, errors.New(message)
	}

	envPath := filepath.Join(appRoot, appmanifest.BTCPayEnvFile)
	envRaw, err := readRegularFile(envPath, 32*1024)
	if err != nil || validateSecretFileMode(envPath) != nil {
		return fail("BTCPay environment is unavailable")
	}
	joinBitcoinNetwork, useTorProxy, err := validateBTCPayEnv(envRaw)
	if err != nil {
		return fail("BTCPay environment does not match the catalog")
	}
	managerPaths := appmanifest.BTCPayComposePaths{
		DataDir:    filepath.Join(dataRoot, "data"),
		NbxDir:     filepath.Join(dataRoot, "nbxplorer"),
		PgDir:      filepath.Join(dataRoot, "pgdata"),
		DbInitPath: filepath.Join(appRoot, appmanifest.BTCPayDBInitFile),
		LndDir:     lndDir,
	}
	composeRaw, err := readRegularFile(filepath.Join(appRoot, appmanifest.BTCPayComposeFile), 96*1024)
	if err != nil {
		return fail("BTCPay compose manifest is unavailable")
	}
	expectedCompose := appmanifest.BTCPayCompose(managerPaths, joinBitcoinNetwork, useTorProxy)
	if !bytes.Equal(composeRaw, []byte(expectedCompose)) {
		return fail("BTCPay compose manifest does not match the catalog")
	}
	dbInitRaw, err := readRegularFile(managerPaths.DbInitPath, 8*1024)
	if err != nil || !bytes.Equal(dbInitRaw, []byte(appmanifest.BTCPayDBInit())) {
		return fail("BTCPay database initialization does not match the catalog")
	}

	certificatePath := filepath.Join(lndDir, appmanifest.BTCPayTLSCertFile)
	certificateRaw, err := readRegularFile(certificatePath, 64*1024)
	if err != nil || validateTLSCertificate(certificateRaw) != nil {
		return fail("BTCPay LND certificate is invalid")
	}
	nativeCertificateRaw, err := readRegularFile(filepath.Join(lndDataRoot, appmanifest.BTCPayTLSCertFile), 64*1024)
	if err != nil || !bytes.Equal(certificateRaw, nativeCertificateRaw) {
		return fail("BTCPay LND certificate does not match the native node")
	}
	macaroonPath := filepath.Join(lndDir, appmanifest.BTCPayMacaroonFile)
	macaroonRaw, err := readRegularFile(macaroonPath, 64*1024)
	if err != nil || validateSecretFileMode(macaroonPath) != nil {
		return fail("BTCPay LND credential is unavailable")
	}
	adminMacaroonPath := filepath.Join(lndDataRoot, "data", "chain", "bitcoin", "mainnet", "admin.macaroon")
	adminMacaroonRaw, err := readRegularFile(adminMacaroonPath, 64*1024)
	if err != nil {
		return fail("native LND admin credential is unavailable")
	}
	if bytes.Equal(macaroonRaw, adminMacaroonRaw) {
		return fail("BTCPay LND credential must not be the admin macaroon")
	}

	files.composeRaw = composeRaw
	files.envRaw = envRaw
	files.dbInitRaw = dbInitRaw
	files.certificateRaw = certificateRaw
	files.macaroonRaw = macaroonRaw
	files.joinBitcoinNetwork = joinBitcoinNetwork
	files.useTorProxy = useTorProxy
	return files, nil
}

// prepareBTCPaySnapshot validates the unprivileged inputs even in dry-run and,
// for execution, persists only a root-owned closed snapshot. The credential is
// deliberately renamed without a .macaroon suffix inside the container.
func (manager *ComposeAppManager) prepareBTCPaySnapshot(dryRun bool) (composeAppSnapshot, func(), error) {
	files, err := manager.validatedBTCPayFiles()
	if err != nil {
		return composeAppSnapshot{}, func() {}, err
	}
	if dryRun {
		return composeAppSnapshot{}, func() {}, nil
	}
	return manager.createBTCPaySnapshot(files)
}

func (manager *ComposeAppManager) Snapshot(ctx context.Context, appID string, dryRun bool) error {
	if manager == nil {
		return errors.New("compose app manager is unavailable")
	}
	if appID != appmanifest.BTCPayID {
		return errors.New("app snapshot manifest is not allowed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, cleanup, err := manager.prepareBTCPaySnapshot(dryRun)
	cleanup()
	return err
}

func (manager *ComposeAppManager) ensureBTCPayNbxplorerDatabase(ctx context.Context, commandPath string, composeArgs []string) error {
	return manager.ensureBTCPayNbxplorerDatabaseWithPolicy(ctx, commandPath, composeArgs, btcpayPostgresReadyTries, btcpayPostgresRetryDelay)
}

func (manager *ComposeAppManager) ensureBTCPayNbxplorerDatabaseWithPolicy(ctx context.Context, commandPath string, composeArgs []string, attempts int, retryDelay time.Duration) error {
	if attempts < 1 || retryDelay < 0 {
		return errors.New("invalid BTCPay postgres readiness policy")
	}
	if commandPath != dockerPath && commandPath != dockerComposePath {
		return errors.New("invalid BTCPay compose command")
	}
	runDatabaseCommand := func(args ...string) (string, error) {
		commandArgs := append([]string(nil), composeArgs...)
		commandArgs = append(commandArgs, "exec", "-T", "btcpay-db")
		commandArgs = append(commandArgs, args...)
		return manager.Runner.Run(ctx, commandPath, commandArgs...)
	}
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, readyErr := runDatabaseCommand("pg_isready", "-U", "btcpay", "-d", "btcpayserver")
		if readyErr == nil {
			output, queryErr := runDatabaseCommand(
				"psql", "-U", "btcpay", "-d", "btcpayserver", "-tAc",
				"SELECT 1 FROM pg_database WHERE datname = 'nbxplorer'",
			)
			if queryErr == nil && strings.TrimSpace(output) == "1" {
				return nil
			}
			if queryErr == nil {
				if _, createErr := runDatabaseCommand(
					"createdb", "-U", "btcpay", "--owner=btcpay", "--template=template0",
					"--encoding=UTF8", "--lc-collate=C", "--lc-ctype=C", "nbxplorer",
				); createErr == nil {
					return nil
				}
			}
		}
		if attempt == attempts-1 {
			return errors.New("BTCPay postgres did not stabilize")
		}
		timer := time.NewTimer(btcpayPostgresRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return errors.New("BTCPay postgres did not stabilize")
}

func (manager *ComposeAppManager) createBTCPaySnapshot(files btcpayValidatedFiles) (composeAppSnapshot, func(), error) {
	var snapshot composeAppSnapshot
	privilegedAppsRoot := manager.PrivilegedAppsRoot
	if privilegedAppsRoot == "" {
		privilegedAppsRoot = defaultPrivilegedAppsRoot
	}
	appsDataRoot := manager.AppsDataRoot
	if appsDataRoot == "" {
		appsDataRoot = defaultAppsDataRoot
	}
	if err := ensureDirectoryTreeNoSymlink(privilegedAppsRoot, 0700); err != nil {
		return snapshot, func() {}, errors.New("failed to secure privileged app root")
	}
	snapshotRoot := filepath.Join(privilegedAppsRoot, appmanifest.BTCPayID)
	if err := ensureDirectoryTreeNoSymlink(snapshotRoot, 0700); err != nil {
		return snapshot, func() {}, errors.New("failed to secure BTCPay execution snapshot")
	}
	lndSnapshotDir := filepath.Join(snapshotRoot, appmanifest.BTCPayLNDDir)
	if err := ensureDirectoryTreeNoSymlink(lndSnapshotDir, 0700); err != nil {
		return snapshot, func() {}, errors.New("failed to secure BTCPay LND snapshot")
	}
	if err := validateSnapshotDirectoryEntries(snapshotRoot, map[string]bool{
		appmanifest.BTCPayComposeFile: true,
		appmanifest.BTCPayEnvFile:     true,
		appmanifest.BTCPayDBInitFile:  true,
		appmanifest.BTCPayLNDDir:      false,
	}); err != nil {
		return snapshot, func() {}, errors.New("BTCPay execution snapshot contains unexpected assets")
	}
	if err := validateSnapshotDirectoryEntries(lndSnapshotDir, map[string]bool{
		appmanifest.BTCPayTLSCertFile:      true,
		appmanifest.BTCPaySnapshotAuthFile: true,
	}); err != nil {
		return snapshot, func() {}, errors.New("BTCPay LND snapshot contains unexpected assets")
	}

	snapshot = composeAppSnapshot{
		root:        snapshotRoot,
		composePath: filepath.Join(snapshotRoot, appmanifest.BTCPayComposeFile),
		envPath:     filepath.Join(snapshotRoot, appmanifest.BTCPayEnvFile),
	}
	dbInitPath := filepath.Join(snapshotRoot, appmanifest.BTCPayDBInitFile)
	certificatePath := filepath.Join(lndSnapshotDir, appmanifest.BTCPayTLSCertFile)
	authPath := filepath.Join(lndSnapshotDir, appmanifest.BTCPaySnapshotAuthFile)
	for _, file := range []struct {
		path    string
		data    []byte
		message string
	}{
		{snapshot.envPath, files.envRaw, "failed to snapshot BTCPay environment"},
		{dbInitPath, files.dbInitRaw, "failed to snapshot BTCPay database initialization"},
		{certificatePath, files.certificateRaw, "failed to snapshot BTCPay LND certificate"},
		{authPath, files.macaroonRaw, "failed to snapshot BTCPay LND credential"},
	} {
		if err := writeAtomicRegularFile(file.path, file.data, 0600); err != nil {
			return composeAppSnapshot{}, func() {}, errors.New(file.message)
		}
	}
	dataRoot := filepath.Join(appsDataRoot, appmanifest.BTCPayID)
	executionPaths := appmanifest.BTCPayComposePaths{
		DataDir:    filepath.Join(dataRoot, "data"),
		NbxDir:     filepath.Join(dataRoot, "nbxplorer"),
		PgDir:      filepath.Join(dataRoot, "pgdata"),
		DbInitPath: dbInitPath,
		LndDir:     lndSnapshotDir,
	}
	executionCompose := appmanifest.BTCPayExecutionCompose(
		executionPaths,
		files.joinBitcoinNetwork,
		files.useTorProxy,
	)
	if strings.Contains(executionCompose, ".macaroon") || strings.Contains(executionCompose, "admin.macaroon") {
		return composeAppSnapshot{}, func() {}, errors.New("BTCPay execution manifest exposes a forbidden LND credential")
	}
	if err := writeAtomicRegularFile(snapshot.composePath, []byte(executionCompose), 0600); err != nil {
		return composeAppSnapshot{}, func() {}, errors.New("failed to snapshot BTCPay compose manifest")
	}
	return snapshot, func() {}, nil
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

func validateBTCPayEnv(raw []byte) (bool, bool, error) {
	if len(raw) == 0 || bytes.Contains(raw, []byte{'\r'}) || raw[len(raw)-1] != '\n' {
		return false, false, errors.New("invalid environment encoding")
	}
	allowed := map[string]int{
		"BTCPAY_DB_PASSWORD":        128,
		"NBXPLORER_BTCRPCURL":       2048,
		"NBXPLORER_BTCRPCUSER":      512,
		"NBXPLORER_BTCRPCPASSWORD":  512,
		"NBXPLORER_BTCNODEENDPOINT": 512,
		"NBXPLORER_SOCKSENDPOINT":   64,
	}
	values := make(map[string]string)
	lines := strings.Split(string(raw), "\n")
	for _, line := range lines[:len(lines)-1] {
		key, value, ok := strings.Cut(line, "=")
		maxBytes, allowedKey := allowed[key]
		if !ok || !allowedKey || value == "" || len(value) > maxBytes {
			return false, false, errors.New("invalid environment entry")
		}
		if _, duplicate := values[key]; duplicate || !isSafeBTCPayEnvValue(value) {
			return false, false, errors.New("invalid environment value")
		}
		values[key] = value
	}
	for _, required := range []string{
		"BTCPAY_DB_PASSWORD",
		"NBXPLORER_BTCRPCURL",
		"NBXPLORER_BTCRPCUSER",
		"NBXPLORER_BTCRPCPASSWORD",
		"NBXPLORER_BTCNODEENDPOINT",
	} {
		if values[required] == "" {
			return false, false, errors.New("missing environment entry")
		}
	}
	if !isBase64URLToken(values["BTCPAY_DB_PASSWORD"], 32) {
		return false, false, errors.New("invalid database credential")
	}
	rpcURL, err := url.Parse(values["NBXPLORER_BTCRPCURL"])
	if err != nil || (rpcURL.Scheme != "http" && rpcURL.Scheme != "https") || rpcURL.User != nil || rpcURL.Hostname() == "" || rpcURL.Port() == "" || rpcURL.Path != "/" || rpcURL.RawQuery != "" || rpcURL.Fragment != "" {
		return false, false, errors.New("invalid Bitcoin RPC URL")
	}
	if !validBTCPayHost(rpcURL.Hostname()) {
		return false, false, errors.New("invalid Bitcoin RPC host")
	}
	rpcPort, err := strconv.Atoi(rpcURL.Port())
	if err != nil || rpcPort < 1 || rpcPort > 65535 {
		return false, false, errors.New("invalid Bitcoin RPC port")
	}
	nodeHost, nodePortRaw, err := net.SplitHostPort(values["NBXPLORER_BTCNODEENDPOINT"])
	if err != nil || !validBTCPayHost(nodeHost) {
		return false, false, errors.New("invalid Bitcoin P2P endpoint")
	}
	nodePort, err := strconv.Atoi(nodePortRaw)
	if err != nil || nodePort < 1 || nodePort > 65535 {
		return false, false, errors.New("invalid Bitcoin P2P port")
	}
	rpcHost := strings.ToLower(rpcURL.Hostname())
	nodeHost = strings.ToLower(nodeHost)
	joinBitcoinNetwork := false
	switch rpcHost {
	case "bitcoind":
		if rpcURL.Scheme != "http" || rpcPort != 8332 || nodeHost != "bitcoind" || nodePort != 8333 {
			return false, false, errors.New("invalid App Store Bitcoin wiring")
		}
		joinBitcoinNetwork = true
	case appmanifest.BitcoinConsumerHostGateway:
		if rpcURL.Scheme != "http" || nodeHost != appmanifest.BitcoinConsumerHostGateway || nodePort != 8333 {
			return false, false, errors.New("invalid native Bitcoin wiring")
		}
		joinBitcoinNetwork = true
	default:
		if nodeHost == "bitcoind" || nodeHost == appmanifest.BitcoinConsumerHostGateway {
			return false, false, errors.New("mixed Bitcoin wiring is not allowed")
		}
	}
	useTorProxy := isBTCPayOnionHost(nodeHost)
	socksEndpoint, hasSocksEndpoint := values["NBXPLORER_SOCKSENDPOINT"]
	if useTorProxy {
		if !hasSocksEndpoint || socksEndpoint != "tor:9050" {
			return false, false, errors.New("invalid Tor wiring")
		}
	} else if hasSocksEndpoint {
		return false, false, errors.New("unexpected Tor wiring")
	}
	return joinBitcoinNetwork, useTorProxy, nil
}

func isSafeBTCPayEnvValue(value string) bool {
	for i := 0; i < len(value); i++ {
		char := value[i]
		if char < 0x21 || char > 0x7e {
			return false
		}
		switch char {
		case '$', '\\', '\'', '"', '`', '#':
			return false
		}
	}
	return true
}

func isBase64URLToken(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' && char != '_' {
			return false
		}
	}
	return true
}

func validBTCPayHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" || len(host) > 253 {
		return false
	}
	if net.ParseIP(host) != nil || isBTCPayOnionHost(host) {
		return true
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

func isBTCPayOnionHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	label := strings.TrimSuffix(host, ".onion")
	if label == host || len(label) != 56 {
		return false
	}
	for _, char := range label {
		if (char < 'a' || char > 'z') && (char < '2' || char > '7') {
			return false
		}
	}
	return true
}

func validateTLSCertificate(certificateRaw []byte) error {
	block, rest := pem.Decode(certificateRaw)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return errors.New("invalid certificate PEM")
	}
	_, err := x509.ParseCertificate(block.Bytes)
	return err
}

func validateSnapshotDirectoryEntries(directory string, allowed map[string]bool) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		expectFile, ok := allowed[entry.Name()]
		if !ok {
			return errors.New("unexpected snapshot entry")
		}
		info, err := os.Lstat(filepath.Join(directory, entry.Name()))
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("invalid snapshot entry")
		}
		if expectFile && !info.Mode().IsRegular() {
			return errors.New("snapshot file is invalid")
		}
		if !expectFile && !info.IsDir() {
			return errors.New("snapshot directory is invalid")
		}
	}
	return nil
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
	if err := securePrivilegedPathOwner(cleanPath); err != nil {
		return err
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
	if err := securePrivilegedPathOwner(temporaryPath); err != nil {
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
