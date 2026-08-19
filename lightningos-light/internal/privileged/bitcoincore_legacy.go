package privileged

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"
)

const (
	bitcoinCoreLegacyRollbackImage      = "lightningos/bitcoin-core-legacy-rollback:0.5.13"
	legacyBitcoinNetworkInspectFormat   = `{{.Driver}}|{{.Scope}}|{{.Internal}}|{{index .Labels "com.docker.compose.project"}}|{{index .Labels "com.docker.compose.network"}}|{{json .IPAM.Config}}|{{json .Containers}}`
	maxLegacyBitcoinNetworkInspectBytes = 256 * 1024
)

type bitcoinCoreRuntime struct {
	ContainerID string
	ImageID     string
	ImageRef    string
	Running     bool
	DataDir     string
	Network     string
	Ports       map[string][]legacyBitcoinPortBinding
}

type legacyBitcoinPortBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type legacyBitcoinNetworkEndpoint struct {
	Name string `json:"Name"`
}

type legacyBitcoinNetworkState struct {
	Subnet         string
	Gateway        string
	ReplaceNetwork bool
}

type bitcoinLegacyMigrationError struct {
	Code    string
	Message string
}

func (err *bitcoinLegacyMigrationError) Error() string {
	if err == nil {
		return ""
	}
	return err.Message
}

type preparedLegacyBitcoinMigration struct {
	runtime      bitcoinCoreRuntime
	network      legacyBitcoinNetworkState
	rollbackBase string
	rollbackRoot string
	composePath  string
}

func (migration *preparedLegacyBitcoinMigration) rollbackComposeArgs(prefix []string, manifest appmanifest.ComposeManifest) []string {
	args := append([]string(nil), prefix...)
	return append(args,
		"--project-name", manifest.Project,
		"--project-directory", migration.rollbackRoot,
		"-f", migration.composePath,
	)
}

func (migration *preparedLegacyBitcoinMigration) cleanup() {
	if migration == nil || migration.rollbackRoot == "" {
		return
	}
	base := filepath.Clean(migration.rollbackBase)
	root := filepath.Clean(migration.rollbackRoot)
	info, err := os.Lstat(root)
	if base == "." || root == "." || filepath.Dir(root) != base ||
		!strings.HasPrefix(filepath.Base(root), "bitcoincore-rollback-") ||
		err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return
	}
	_ = os.RemoveAll(root)
}

func (manager *ComposeAppManager) prepareLegacyBitcoinMigration(ctx context.Context) (*preparedLegacyBitcoinMigration, error) {
	runtimeState, found, err := manager.inspectBitcoinCoreCatalogRuntime(ctx)
	if err != nil || !found {
		return nil, err
	}
	if runtimeState.ImageRef == appmanifest.BitcoinCoreImage {
		return nil, nil
	}
	if !isLegacyBitcoinCoreImage(runtimeState.ImageRef) {
		return nil, errors.New("Bitcoin Core runtime image is not eligible for automatic migration")
	}
	root := manager.PrivilegedAppsRoot
	if root == "" {
		root = defaultPrivilegedAppsRoot
	}
	expectedDataDir, err := readStorageMetadata(filepath.Join(root, appmanifest.BitcoinCoreID, bitcoinCoreStorageDataDirFile))
	if err != nil || filepath.Clean(runtimeState.DataDir) != filepath.Clean(expectedDataDir) {
		return nil, errors.New("Bitcoin Core legacy storage does not match the enrolled volume")
	}
	if runtimeState.Network != appmanifest.BitcoinCoreNetwork {
		return nil, errors.New("Bitcoin Core legacy network is not eligible for automatic migration")
	}
	networkState, err := manager.inspectLegacyBitcoinNetwork(ctx, runtimeState)
	if err != nil {
		return nil, err
	}
	portLines, err := validatedLegacyBitcoinPortLines(runtimeState.Ports)
	if err != nil {
		return nil, err
	}
	if _, err := manager.Runner.Run(ctx, dockerPath, "image", "inspect", runtimeState.ImageID); err != nil {
		return nil, errors.New("Bitcoin Core legacy image is unavailable")
	}
	rollbackRootBase := manager.TempRoot
	if rollbackRootBase == "" {
		rollbackRootBase = os.TempDir()
	}
	rollbackRootBase, err = filepath.Abs(filepath.Clean(rollbackRootBase))
	if err != nil {
		return nil, errors.New("Bitcoin Core rollback workspace location is invalid")
	}
	rollbackRoot, err := os.MkdirTemp(rollbackRootBase, "bitcoincore-rollback-")
	if err != nil {
		return nil, errors.New("Bitcoin Core rollback workspace could not be created")
	}
	migration := &preparedLegacyBitcoinMigration{
		runtime: runtimeState, network: networkState, rollbackBase: rollbackRootBase, rollbackRoot: rollbackRoot,
		composePath: filepath.Join(rollbackRoot, "docker-compose.yaml"),
	}
	if err := os.Chmod(rollbackRoot, 0700); err != nil {
		migration.cleanup()
		return nil, errors.New("Bitcoin Core rollback workspace could not be secured")
	}
	rollbackCompose := legacyBitcoinRollbackCompose(expectedDataDir, portLines, networkState)
	if err := os.WriteFile(migration.composePath, []byte(rollbackCompose), 0600); err != nil {
		migration.cleanup()
		return nil, errors.New("Bitcoin Core rollback manifest could not be prepared")
	}
	commandPath, prefix, err := manager.resolveCompose(ctx)
	if err != nil {
		migration.cleanup()
		return nil, err
	}
	manifest, _ := appmanifest.ComposeManifestForApp(appmanifest.BitcoinCoreID)
	validateArgs := append(migration.rollbackComposeArgs(prefix, manifest), "config", "--quiet")
	if _, err := manager.Runner.Run(ctx, commandPath, validateArgs...); err != nil {
		migration.cleanup()
		return nil, errors.New("Bitcoin Core rollback manifest validation failed")
	}
	if err := manager.captureLegacyBitcoinRollbackImage(ctx, runtimeState.ContainerID); err != nil {
		migration.cleanup()
		return nil, err
	}
	return migration, nil
}

func (manager *ComposeAppManager) captureLegacyBitcoinRollbackImage(ctx context.Context, containerID string) error {
	if _, err := manager.Runner.Run(ctx, dockerPath, "commit", "--pause=false", containerID, bitcoinCoreLegacyRollbackImage); err != nil {
		return bitcoinLegacyRollbackCaptureError()
	}
	rollbackImageID, err := manager.Runner.Run(ctx, dockerPath, "image", "inspect", "--format", "{{.Id}}", bitcoinCoreLegacyRollbackImage)
	if err != nil || inspectedDockerImageID(rollbackImageID) == "" {
		return bitcoinLegacyRollbackCaptureError()
	}
	return nil
}

func inspectedDockerImageID(output string) string {
	imageID := ""
	for _, line := range strings.Split(output, "\n") {
		candidate := strings.TrimSpace(line)
		if !dockerImageIDPattern.MatchString(candidate) {
			continue
		}
		if imageID != "" && imageID != candidate {
			return ""
		}
		imageID = candidate
	}
	return imageID
}

func bitcoinLegacyRollbackCaptureError() error {
	return &bitcoinLegacyMigrationError{
		Code:    "bitcoin_legacy_rollback_capture_failed",
		Message: "Bitcoin Core migration did not start because the rollback image could not be verified; the legacy runtime remains unchanged.",
	}
}

func (manager *ComposeAppManager) inspectLegacyBitcoinNetwork(ctx context.Context, runtimeState bitcoinCoreRuntime) (legacyBitcoinNetworkState, error) {
	output, err := manager.Runner.Run(ctx, dockerPath,
		"network", "inspect", appmanifest.BitcoinCoreNetwork,
		"--format", legacyBitcoinNetworkInspectFormat,
	)
	if err != nil || len(output) == 0 || len(output) > maxLegacyBitcoinNetworkInspectBytes {
		return legacyBitcoinNetworkState{}, errors.New("Bitcoin Core legacy network inspection failed")
	}
	parts := strings.Split(strings.TrimSpace(output), "|")
	if len(parts) != 7 || parts[0] != "bridge" || parts[1] != "local" || parts[2] != "false" ||
		parts[3] != appmanifest.BitcoinCoreProject || parts[4] != "default" {
		return legacyBitcoinNetworkState{}, errors.New("Bitcoin Core legacy network is not eligible for automatic migration")
	}
	var configs []bitcoinConsumerIPAMConfig
	if json.Unmarshal([]byte(parts[5]), &configs) != nil || len(configs) != 1 {
		return legacyBitcoinNetworkState{}, errors.New("Bitcoin Core legacy network IPAM is invalid")
	}
	subnet := strings.TrimSpace(configs[0].Subnet)
	gateway := strings.TrimSpace(configs[0].Gateway)
	gatewayIP := net.ParseIP(gateway)
	_, parsedSubnet, parseErr := net.ParseCIDR(subnet)
	if parseErr != nil || parsedSubnet.String() != subnet || gatewayIP == nil || gatewayIP.To4() == nil ||
		!gatewayIP.IsPrivate() || !parsedSubnet.Contains(gatewayIP) {
		return legacyBitcoinNetworkState{}, errors.New("Bitcoin Core legacy network IPAM is invalid")
	}
	var endpoints map[string]legacyBitcoinNetworkEndpoint
	if json.Unmarshal([]byte(parts[6]), &endpoints) != nil {
		return legacyBitcoinNetworkState{}, errors.New("Bitcoin Core legacy network endpoints are invalid")
	}
	replaceNetwork := subnet != appmanifest.BitcoinConsumerRPCSubnet || gateway != appmanifest.BitcoinConsumerHostGateway
	if replaceNetwork {
		for containerID := range endpoints {
			if parseDockerContainerID(containerID) == "" || containerID != runtimeState.ContainerID {
				return legacyBitcoinNetworkState{}, &bitcoinLegacyMigrationError{
					Code:    "bitcoin_legacy_apps_running",
					Message: "Stop Electrs, Mempool, and other Bitcoin-dependent App Store apps before retrying the Bitcoin Core migration.",
				}
			}
		}
	}
	return legacyBitcoinNetworkState{Subnet: subnet, Gateway: gateway, ReplaceNetwork: replaceNetwork}, nil
}

func validatedLegacyBitcoinPortLines(bindings map[string][]legacyBitcoinPortBinding) ([]string, error) {
	allowed := map[string]string{"8332/tcp": "8332", "8333/tcp": "8333", "28332/tcp": "28332", "28333/tcp": "28333"}
	lines := make([]string, 0, len(bindings))
	for containerPort, entries := range bindings {
		port, ok := allowed[containerPort]
		if !ok {
			return nil, errors.New("Bitcoin Core legacy port topology is not eligible for automatic migration")
		}
		for _, entry := range entries {
			if entry.HostPort != port {
				return nil, errors.New("Bitcoin Core legacy port topology is not eligible for automatic migration")
			}
			hostIP := strings.TrimSpace(entry.HostIP)
			switch hostIP {
			case "", "0.0.0.0", "::", "127.0.0.1":
			default:
				return nil, errors.New("Bitcoin Core legacy port topology is not eligible for automatic migration")
			}
			binding := port + ":" + port
			if hostIP != "" && hostIP != "0.0.0.0" {
				binding = hostIP + ":" + binding
			}
			lines = append(lines, "      - \""+binding+"\"")
		}
	}
	sort.Strings(lines)
	return lines, nil
}

func legacyBitcoinRollbackCompose(dataDir string, portLines []string, network legacyBitcoinNetworkState) string {
	ports := ""
	if len(portLines) > 0 {
		ports = "\n    ports:\n" + strings.Join(portLines, "\n")
	}
	return fmt.Sprintf(`services:
  bitcoind:
    image: %s
    restart: unless-stopped%s
    volumes:
      - %s:%s
networks:
  default:
    name: %s
    driver: bridge
    ipam:
      config:
        - subnet: %s
          gateway: %s
`, bitcoinCoreLegacyRollbackImage, ports, dataDir, appmanifest.BitcoinCoreContainerDataDir,
		appmanifest.BitcoinCoreNetwork, network.Subnet, network.Gateway)
}

func (manager *ComposeAppManager) rollbackLegacyBitcoinMigration(ctx context.Context, commandPath string, prefix []string, executionArgs []string, manifest appmanifest.ComposeManifest, migration *preparedLegacyBitcoinMigration, cause error) error {
	rollbackCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if migration.network.ReplaceNetwork && len(executionArgs) > 0 {
		cleanupArgs := append(append([]string(nil), executionArgs...), "down", "--remove-orphans", "--timeout", strconv.Itoa(manifest.StopTimeoutSeconds))
		_, _ = manager.Runner.Run(rollbackCtx, commandPath, cleanupArgs...)
	}
	rollbackArgs := migration.rollbackComposeArgs(prefix, manifest)
	rollbackArgs = append(rollbackArgs,
		"up", "-d", "--force-recreate", "--no-deps", manifest.PrimaryService,
	)
	if _, err := manager.Runner.Run(rollbackCtx, commandPath, rollbackArgs...); err != nil {
		return &bitcoinLegacyMigrationError{
			Code:    "bitcoin_legacy_rollback_failed",
			Message: "Bitcoin Core migration failed and automatic rollback could not restore the legacy runtime; check local broker logs before taking action.",
		}
	}
	containerID, err := manager.catalogRunningContainerID(rollbackCtx, manifest)
	if err != nil || containerID == "" || manager.probeBitcoinCoreMainnet(rollbackCtx, containerID) != nil {
		return &bitcoinLegacyMigrationError{
			Code:    "bitcoin_legacy_rollback_unverified",
			Message: "Bitcoin Core migration failed and automatic rollback could not verify the restored legacy runtime; check local broker logs before taking action.",
		}
	}
	_ = cause
	return &bitcoinLegacyMigrationError{
		Code:    "bitcoin_legacy_migration_rolled_back",
		Message: "Bitcoin Core migration failed; the legacy runtime was restored automatically and LND was not restarted.",
	}
}

func (manager *ComposeAppManager) probeBitcoinCoreMainnet(ctx context.Context, containerID string) error {
	output, err := manager.Runner.Run(ctx, dockerPath, "exec", "-i", containerID,
		"bitcoin-cli", "-datadir="+appmanifest.BitcoinCoreContainerDataDir,
		"-conf="+appmanifest.BitcoinCoreContainerConfig, "-rpcwait", "-rpcwaittimeout=60", "getblockchaininfo")
	if err != nil || len(output) == 0 || len(output) > 64*1024 {
		return errors.New("Bitcoin Core RPC did not become ready")
	}
	var state struct {
		Chain string `json:"chain"`
	}
	if json.Unmarshal([]byte(output), &state) != nil || state.Chain != "main" {
		return errors.New("Bitcoin Core RPC did not confirm mainnet")
	}
	return nil
}

func (manager *ComposeAppManager) probeMigratedBitcoinCore(ctx context.Context, containerID string) error {
	if err := manager.probeBitcoinCoreMainnet(ctx, containerID); err != nil {
		return err
	}
	for _, port := range []int{8332, 28332, 28333} {
		connection, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			return errors.New("Bitcoin Core loopback RPC/ZMQ endpoints did not become ready")
		}
		_ = connection.Close()
	}
	return nil
}

func (manager *ComposeAppManager) inspectBitcoinCoreCatalogRuntime(ctx context.Context) (bitcoinCoreRuntime, bool, error) {
	manifest, _ := appmanifest.ComposeManifestForApp(appmanifest.BitcoinCoreID)
	output, err := manager.Runner.Run(ctx, dockerPath,
		"ps", "-a",
		"--filter", "label=com.docker.compose.project="+manifest.Project,
		"--filter", "label=com.docker.compose.service="+manifest.PrimaryService,
		"--format", "{{.ID}}",
	)
	if err != nil {
		return bitcoinCoreRuntime{}, false, errors.New("Bitcoin Core runtime lookup failed")
	}
	containerID := ""
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parsed := parseDockerContainerID(line)
		if parsed == "" || containerID != "" {
			return bitcoinCoreRuntime{}, false, errors.New("Bitcoin Core runtime lookup returned invalid container IDs")
		}
		containerID = parsed
	}
	if containerID == "" {
		return bitcoinCoreRuntime{}, false, nil
	}

	raw, err := manager.Runner.Run(ctx, dockerPath, "inspect", containerID)
	if err != nil || len(raw) == 0 || len(raw) > 256*1024 {
		return bitcoinCoreRuntime{}, false, errors.New("Bitcoin Core runtime inspection failed")
	}
	var payload []struct {
		ID     string `json:"Id"`
		Image  string `json:"Image"`
		Config struct {
			Image string `json:"Image"`
		} `json:"Config"`
		State struct {
			Running bool `json:"Running"`
		} `json:"State"`
		HostConfig struct {
			NetworkMode  string                                `json:"NetworkMode"`
			PortBindings map[string][]legacyBitcoinPortBinding `json:"PortBindings"`
		} `json:"HostConfig"`
		Mounts []struct {
			Type        string `json:"Type"`
			Source      string `json:"Source"`
			Destination string `json:"Destination"`
			RW          bool   `json:"RW"`
		} `json:"Mounts"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || len(payload) != 1 {
		return bitcoinCoreRuntime{}, false, errors.New("Bitcoin Core runtime inspection returned invalid data")
	}
	item := payload[0]
	containerID = parseDockerContainerID(item.ID)
	if containerID == "" || !dockerImageIDPattern.MatchString(item.Image) {
		return bitcoinCoreRuntime{}, false, errors.New("Bitcoin Core runtime identity is invalid")
	}
	runtime := bitcoinCoreRuntime{
		ContainerID: containerID,
		ImageID:     item.Image,
		ImageRef:    strings.TrimSpace(item.Config.Image),
		Running:     item.State.Running,
		Network:     strings.TrimSpace(item.HostConfig.NetworkMode),
		Ports:       item.HostConfig.PortBindings,
	}
	for _, mount := range item.Mounts {
		if mount.Destination == appmanifest.BitcoinCoreContainerDataDir {
			if runtime.DataDir != "" || mount.Type != "bind" || !mount.RW {
				return bitcoinCoreRuntime{}, false, errors.New("Bitcoin Core runtime storage is invalid")
			}
			runtime.DataDir = mount.Source
		}
	}
	if runtime.DataDir == "" {
		return bitcoinCoreRuntime{}, false, errors.New("Bitcoin Core runtime storage is unavailable")
	}
	return runtime, true, nil
}

func isLegacyBitcoinCoreImage(image string) bool {
	switch image {
	case "bitcoin/bitcoin:latest", bitcoinCoreLegacyRollbackImage:
		return true
	default:
		return false
	}
}
