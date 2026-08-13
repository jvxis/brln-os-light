package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/system"
)

type bitcoinCorePaths struct {
	Root        string
	DataDir     string
	ConfigPath  string
	ComposePath string
}

type bitcoinCoreApp struct {
	server *Server
}

const (
	bitcoinCoreAppID            = appmanifest.BitcoinCoreID
	bitcoinCoreImage            = appmanifest.BitcoinCoreImage
	bitcoinCoreDefaultDataDir   = appmanifest.BitcoinCoreDefaultDataDir
	bitcoinCoreDataDirStateFile = "data_dir"
	bitcoinCoreStorageIDFile    = "storage_id"
	bitcoinCoreStorageMarker    = appmanifest.BitcoinCoreStorageMarker
	bitcoinCoreConfigFile       = "bitcoin.conf"
	bitcoinCoreMinFreeKiB       = 10 * 1024 * 1024
	bitcoinCoreLegacySeedMax    = 8 * 1024
)

type bitcoinCoreInstallOptions struct {
	DataDir      string `json:"data_dir"`
	StorageMount string `json:"storage_mount"`
}

func newBitcoinCoreApp(s *Server) appHandler {
	return bitcoinCoreApp{server: s}
}

func bitcoincoreDefinition() appDefinition {
	return appDefinition{
		ID:          bitcoinCoreAppID,
		Name:        "Bitcoin Core",
		Description: "Run a local Bitcoin Core node with Docker.",
		Port:        0,
	}
}

func (a bitcoinCoreApp) Definition() appDefinition {
	return bitcoincoreDefinition()
}

func (a bitcoinCoreApp) Info(ctx context.Context) (appInfo, error) {
	def := a.Definition()
	info := newAppInfo(def)
	paths := bitcoinCoreAppPaths()
	if !fileExists(paths.ComposePath) {
		return info, nil
	}
	info.Installed = true
	status, err := inspectBitcoinCoreStatus(ctx)
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func (a bitcoinCoreApp) Install(ctx context.Context) error {
	return a.server.installBitcoinCore(ctx)
}

func (a bitcoinCoreApp) Uninstall(ctx context.Context) error {
	return a.server.uninstallBitcoinCore(ctx)
}

func (a bitcoinCoreApp) Start(ctx context.Context) error {
	return a.server.startBitcoinCore(ctx)
}

func (a bitcoinCoreApp) Stop(ctx context.Context) error {
	return a.server.stopBitcoinCore(ctx)
}

func bitcoinCoreAppPaths() bitcoinCorePaths {
	root := filepath.Join(appsRoot, bitcoinCoreAppID)
	dataDir := readBitcoinCoreDataDir()
	return bitcoinCorePaths{
		Root:        root,
		DataDir:     dataDir,
		ConfigPath:  filepath.Join(dataDir, "bitcoin.conf"),
		ComposePath: filepath.Join(root, "docker-compose.yaml"),
	}
}

func bitcoinCoreAppDataDir() string {
	return filepath.Join(appsDataRoot, bitcoinCoreAppID)
}

func bitcoinCoreDataDirStatePath() string {
	return filepath.Join(bitcoinCoreAppDataDir(), bitcoinCoreDataDirStateFile)
}

func readBitcoinCoreDataDir() string {
	raw, err := os.ReadFile(bitcoinCoreDataDirStatePath())
	if err != nil {
		return bitcoinCoreDefaultDataDir
	}
	normalized, err := normalizeBitcoinCoreDataDir(string(raw))
	if err != nil {
		return bitcoinCoreDefaultDataDir
	}
	return normalized
}

func writeBitcoinCoreDataDir(dataDir string) error {
	normalized, err := normalizeBitcoinCoreDataDir(dataDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(bitcoinCoreAppDataDir(), 0750); err != nil {
		return err
	}
	return writeFile(bitcoinCoreDataDirStatePath(), normalized+"\n", 0640)
}

func normalizeBitcoinCoreDataDir(dataDir string) (string, error) {
	return appmanifest.NormalizeBitcoinCoreDataDir(dataDir)
}

func bitcoinCoreBlockedDataDirPrefixes() []string {
	return []string{
		"/bin",
		"/boot",
		"/data/bitcoin",
		"/data/elements",
		"/data/lnd",
		"/dev",
		"/etc",
		"/home",
		"/lib",
		"/lib64",
		"/proc",
		"/root",
		"/run",
		"/sbin",
		"/sys",
		"/tmp",
		"/usr",
		"/var",
	}
}

func (s *Server) installBitcoinCore(ctx context.Context) error {
	return s.installBitcoinCoreWithOptions(ctx, bitcoinCoreInstallOptions{})
}

func (s *Server) installBitcoinCoreWithOptions(ctx context.Context, opts bitcoinCoreInstallOptions) error {
	requestedDataDir, dataDirRequested, err := resolveBitcoinCoreInstallDataDir(ctx, opts)
	if err != nil {
		return err
	}
	if dataDirRequested {
		normalized, err := normalizeBitcoinCoreDataDir(requestedDataDir)
		if err != nil {
			return err
		}
		currentPaths := bitcoinCoreAppPaths()
		if fileExists(currentPaths.ComposePath) && normalized != currentPaths.DataDir {
			return errors.New("Bitcoin Core data directory cannot be changed after installation")
		}
		if normalized == bitcoinCoreDefaultDataDir {
			if err := writeBitcoinCoreDataDir(normalized); err != nil {
				return err
			}
		} else {
			if err := validateBitcoinCoreInstallDataDir(ctx, normalized); err != nil {
				return err
			}
			if err := writeBitcoinCoreDataDir(normalized); err != nil {
				return err
			}
		}
	}

	paths := bitcoinCoreAppPaths()
	if !dataDirRequested && paths.DataDir != bitcoinCoreDefaultDataDir {
		if err := validateBitcoinCoreInstallDataDir(ctx, paths.DataDir); err != nil {
			return err
		}
	}

	if err := ensureDockerForCatalogApp(ctx); err != nil {
		return err
	}
	if err := ensureBitcoinConsumerNetwork(ctx); err != nil {
		return err
	}
	if err := ensureBitcoinCoreImage(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.Root, 0750); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}
	if err := ensureBitcoinCoreStorageGuard(ctx, paths); err != nil {
		return err
	}
	if err := ensureBitcoinCoreConfig(ctx, paths); err != nil {
		return err
	}
	wasRunning, configChanged, err := ensureBitcoinCoreConsumerRPCBaseline(ctx, paths)
	if err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, bitcoinCoreComposeContents(paths)); err != nil {
		return err
	}
	if err := runBitcoinCoreLifecycle(ctx, "start"); err != nil {
		return err
	}
	if wasRunning && configChanged {
		if err := runBitcoinCoreLifecycle(ctx, "restart"); err != nil {
			return fmt.Errorf("failed to restart Bitcoin Core after one-time RPC baseline migration: %w", err)
		}
	}
	return nil
}

func resolveBitcoinCoreInstallDataDir(ctx context.Context, opts bitcoinCoreInstallOptions) (string, bool, error) {
	requestedDataDir := strings.TrimSpace(opts.DataDir)
	requestedMount := strings.TrimSpace(opts.StorageMount)
	if requestedDataDir != "" && requestedMount != "" {
		return "", false, errors.New("choose either data_dir or storage_mount for Bitcoin Core installation")
	}
	if requestedMount != "" {
		dataDir, err := resolveInstallDataDirFromStorageMount(ctx, bitcoinCoreAppID, requestedMount)
		if err != nil {
			return "", false, err
		}
		return dataDir, true, nil
	}
	if requestedDataDir != "" {
		return requestedDataDir, true, nil
	}
	return "", false, nil
}

func validateBitcoinCoreInstallDataDir(ctx context.Context, dataDir string) error {
	normalized, err := normalizeBitcoinCoreDataDir(dataDir)
	if err != nil {
		return err
	}
	if handled, err := system.EnsureBitcoinCoreStorageWithBroker(ctx, normalized); handled {
		if err != nil {
			return fmt.Errorf("bitcoin storage enrollment failed: %w", err)
		}
		return nil
	}
	return errors.New("bitcoin storage enrollment requires privileged broker enforce mode")
}

func (s *Server) uninstallBitcoinCore(ctx context.Context) error {
	paths := bitcoinCoreAppPaths()
	if fileExists(paths.ComposePath) {
		if handled, err := system.RemoveAppWithBroker(ctx, appmanifest.BitcoinCoreID); !handled {
			return errors.New("Bitcoin Core removal requires privileged broker enforce mode")
		} else if err != nil {
			return fmt.Errorf("Bitcoin Core removal failed: %w", err)
		}
	}
	if err := os.RemoveAll(paths.Root); err != nil {
		return fmt.Errorf("failed to remove app files: %w", err)
	}
	return nil
}

func (s *Server) startBitcoinCore(ctx context.Context) error {
	paths := bitcoinCoreAppPaths()
	if paths.DataDir != bitcoinCoreDefaultDataDir {
		if err := validateBitcoinCoreInstallDataDir(ctx, paths.DataDir); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(paths.Root, 0750); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}
	if err := ensureBitcoinCoreImage(ctx); err != nil {
		return err
	}
	if err := ensureBitcoinCoreStorageGuard(ctx, paths); err != nil {
		return err
	}
	if err := ensureBitcoinCoreConfig(ctx, paths); err != nil {
		return err
	}
	wasRunning, configChanged, err := ensureBitcoinCoreConsumerRPCBaseline(ctx, paths)
	if err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, bitcoinCoreComposeContents(paths)); err != nil {
		return err
	}
	if err := runBitcoinCoreLifecycle(ctx, "start"); err != nil {
		return err
	}
	if wasRunning && configChanged {
		if err := runBitcoinCoreLifecycle(ctx, "restart"); err != nil {
			return fmt.Errorf("failed to restart Bitcoin Core after one-time RPC baseline migration: %w", err)
		}
	}
	return nil
}

func (s *Server) stopBitcoinCore(ctx context.Context) error {
	paths := bitcoinCoreAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("Bitcoin Core is not installed")
	}
	return runBitcoinCoreLifecycle(ctx, "stop")
}

func bitcoinCoreComposeContents(paths bitcoinCorePaths) string {
	raw, err := appmanifest.BitcoinCoreCompose(paths.DataDir, appmanifest.BitcoinCoreExecutionRoot)
	if err != nil {
		return ""
	}
	return raw
}

func ensureBitcoinCoreStorageGuard(ctx context.Context, paths bitcoinCorePaths) error {
	if handled, err := system.EnsureBitcoinCoreStorageWithBroker(ctx, paths.DataDir); handled {
		if err != nil {
			return fmt.Errorf("bitcoin storage enrollment failed: %w", err)
		}
		return nil
	}
	return errors.New("Bitcoin Core storage enrollment requires privileged broker enforce mode")
}

func bitcoinCoreStorageGuardContents() string {
	return appmanifest.BitcoinCoreStorageGuard()
}

func runBitcoinCoreLifecycle(ctx context.Context, action string) error {
	if handled, err := system.AppLifecycleWithBroker(ctx, appmanifest.BitcoinCoreID, action); !handled {
		return errors.New("Bitcoin Core lifecycle requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("Bitcoin Core %s failed: %w", action, err)
	}
	return nil
}

func inspectBitcoinCoreStatus(ctx context.Context) (string, error) {
	if handled, status, _, err := system.InspectAppWithBroker(ctx, appmanifest.BitcoinCoreID); !handled {
		return "unknown", errors.New("Bitcoin Core status requires privileged broker enforce mode")
	} else if err != nil {
		return "unknown", fmt.Errorf("Bitcoin Core status failed: %w", err)
	} else {
		return status, nil
	}
}

func ensureBitcoinCoreConfig(ctx context.Context, paths bitcoinCorePaths) error {
	content, err := defaultBitcoinCoreConfig()
	if err != nil {
		return err
	}
	legacyContent, legacyExists, err := readLegacyBitcoinCoreSeedConfig(paths.Root)
	if err != nil {
		return err
	}
	if legacyExists {
		content = ensureTrailingNewline(legacyContent)
	}
	if handled, err := system.EnsureBitcoinCoreConfigWithBroker(ctx, paths.DataDir, content, !legacyExists); handled {
		if err != nil {
			return fmt.Errorf("failed to ensure bitcoin.conf: %w", err)
		}
		if legacyExists {
			if err := removeLegacyBitcoinCoreSeedConfig(paths.Root); err != nil {
				return err
			}
		}
		return nil
	}
	return errors.New("bitcoin.conf management requires privileged broker enforce mode")
}

func readLegacyBitcoinCoreSeedConfig(root string) (string, bool, error) {
	path := filepath.Join(root, bitcoinCoreConfigFile)
	before, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > bitcoinCoreLegacySeedMax {
		return "", false, errors.New("legacy bitcoin.conf seed is unsafe")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", false, errors.New("legacy bitcoin.conf seed is unreadable")
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || !after.Mode().IsRegular() || after.Size() <= 0 || after.Size() > bitcoinCoreLegacySeedMax {
		return "", false, errors.New("legacy bitcoin.conf seed changed during read")
	}
	raw, err := io.ReadAll(io.LimitReader(file, bitcoinCoreLegacySeedMax+1))
	if err != nil || len(raw) > bitcoinCoreLegacySeedMax {
		return "", false, errors.New("legacy bitcoin.conf seed read failed")
	}
	return string(raw), true, nil
}

func removeLegacyBitcoinCoreSeedConfig(root string) error {
	path := filepath.Join(root, bitcoinCoreConfigFile)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("legacy bitcoin.conf seed cleanup target is unsafe")
	}
	if err := os.Remove(path); err != nil {
		return errors.New("legacy bitcoin.conf seed cleanup failed")
	}
	return nil
}

func defaultBitcoinCoreConfig() (string, error) {
	lines := []string{
		"server=1",
		"printtoconsole=1",
		"txindex=1",
		"natpmp=0",
		"upnp=0",
		"rpcbind=0.0.0.0:8332",
		"rpcallowip=127.0.0.1",
		"rpcallowip=" + appmanifest.BitcoinCoreRPCSubnet,
		"whitelist=" + appmanifest.BitcoinConsumerRPCSubnet,
		"zmqpubrawblock=tcp://0.0.0.0:28332",
		"zmqpubrawtx=tcp://0.0.0.0:28333",
		"",
	}
	return strings.Join(lines, "\n"), nil
}

func ensureBitcoinCoreConsumerRPCBaseline(ctx context.Context, paths bitcoinCorePaths) (bool, bool, error) {
	wasRunning := false
	if fileExists(paths.ComposePath) {
		status, err := inspectBitcoinCoreStatus(ctx)
		if err != nil {
			return false, false, fmt.Errorf("failed to inspect Bitcoin Core before RPC baseline migration: %w", err)
		}
		wasRunning = status == "running"
	}

	raw, err := readBitcoinCoreConfigRaw(ctx, paths)
	if err != nil {
		return false, false, err
	}
	updated, changed := ensureBitcoinCoreConsumerRPCValues(raw)
	if !changed {
		return wasRunning, false, nil
	}
	if err := writeBitcoinCoreConfig(ctx, paths, updated); err != nil {
		return false, false, err
	}
	return wasRunning, true, nil
}

func ensureBitcoinCoreConsumerRPCValues(raw string) (string, bool) {
	normalized := sanitizeBitcoinCoreConfig(raw)
	lines := strings.Split(strings.TrimRight(normalized, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	changed := normalized != ensureTrailingNewline(strings.TrimRight(strings.ReplaceAll(raw, "\r\n", "\n"), "\n"))
	sectionIndex := len(lines)
	for index, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			sectionIndex = index
			break
		}
	}
	requiredValues := []struct {
		key   string
		value string
	}{
		{key: "rpcallowip", value: "127.0.0.1"},
		{key: "rpcallowip", value: appmanifest.BitcoinCoreRPCSubnet},
		{key: "whitelist", value: appmanifest.BitcoinConsumerRPCSubnet},
	}
	for _, required := range requiredValues {
		found := false
		for _, line := range lines[:sectionIndex] {
			key, value, ok := bitcoinCoreConfigKeyValue(line)
			if ok && strings.EqualFold(key, required.key) && value == required.value {
				found = true
				break
			}
		}
		if !found {
			lines = append(lines, "")
			copy(lines[sectionIndex+1:], lines[sectionIndex:])
			lines[sectionIndex] = required.key + "=" + required.value
			sectionIndex++
			changed = true
		}
	}
	for _, key := range []string{"natpmp", "upnp"} {
		found := false
		for index := 0; index < sectionIndex; index++ {
			currentKey, value, ok := bitcoinCoreConfigKeyValue(lines[index])
			if !ok || !strings.EqualFold(currentKey, key) {
				continue
			}
			found = true
			if value != "0" {
				lines[index] = key + "=0"
				changed = true
			}
		}
		if !found {
			lines = append(lines, "")
			copy(lines[sectionIndex+1:], lines[sectionIndex:])
			lines[sectionIndex] = key + "=0"
			sectionIndex++
			changed = true
		}
	}
	if !changed {
		return normalized, false
	}
	return ensureTrailingNewline(strings.Join(lines, "\n")), true
}

func ensureBitcoinCoreImage(ctx context.Context) error {
	if handled, err := system.PrepareAppImageWithBroker(ctx, appmanifest.BitcoinCoreID, string(appmanifest.BitcoinCoreImageNode)); handled {
		if err != nil {
			return fmt.Errorf("bitcoin core image unavailable: %w", err)
		}
		return nil
	}
	return errors.New("verified Bitcoin Core image requires privileged broker enforce mode")
}
