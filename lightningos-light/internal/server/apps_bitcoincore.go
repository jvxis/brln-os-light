package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/system"
)

type bitcoinCorePaths struct {
	Root             string
	DataDir          string
	ConfigPath       string
	ComposePath      string
	StorageIDPath    string
	StorageGuardPath string
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
	bitcoinCoreStorageGuardFile = "storage-guard.sh"
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
	status, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, "bitcoind")
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
		Root:             root,
		DataDir:          dataDir,
		ConfigPath:       filepath.Join(dataDir, "bitcoin.conf"),
		ComposePath:      filepath.Join(root, "docker-compose.yaml"),
		StorageIDPath:    appmanifest.BitcoinCoreStorageIDPath,
		StorageGuardPath: filepath.Join(root, bitcoinCoreStorageGuardFile),
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
	if _, err := ensureFileWithChange(paths.ComposePath, bitcoinCoreComposeContents(paths)); err != nil {
		return err
	}
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
		return err
	}
	if _, changed, err := syncBitcoinCoreRPCAllowList(ctx, paths); err != nil {
		return err
	} else if changed {
		if err := runCompose(ctx, paths.Root, paths.ComposePath, "restart", "bitcoind"); err != nil {
			return err
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
	parent := path.Dir(normalized)
	script := fmt.Sprintf(`set -e
parent=%s
data=%s
nearest="$parent"
while [ ! -e "$nearest" ] && [ "$nearest" != "/" ]; do
  nearest="$(dirname "$nearest")"
done
if [ ! -d "$nearest" ]; then
  echo "nearest existing parent is not a directory: $nearest" >&2
  exit 10
fi
if command -v findmnt >/dev/null 2>&1; then
  mount_target="$(findmnt -T "$nearest" -no TARGET 2>/dev/null | head -n1 || true)"
  if [ -z "$mount_target" ]; then
    echo "parent directory is not on a mounted filesystem: $nearest" >&2
    exit 11
  fi
  if [ "$mount_target" = "/" ]; then
    echo "parent directory is on the root filesystem; mount the target volume first: $parent" >&2
    exit 12
  fi
fi
mkdir -p "$data"
if [ ! -d "$data" ]; then
  echo "data directory is not a directory: $data" >&2
  exit 13
fi
touch "$data/.lightningos-write-test"
rm -f "$data/.lightningos-write-test"
available="$(df -Pk "$data" | awk 'NR==2 {print $4}')"
if [ -z "$available" ]; then
  echo "could not determine free space in $data" >&2
  exit 14
fi
if [ "$available" -lt %d ]; then
  echo "not enough free space in $data" >&2
  exit 15
fi
`, shellQuote(parent), shellQuote(normalized), bitcoinCoreMinFreeKiB)
	out, err := runSystemd(ctx, "/bin/sh", "-c", script)
	if err != nil {
		msg := strings.TrimSpace(out)
		if msg == "" {
			return fmt.Errorf("bitcoin data_dir validation failed: %w", err)
		}
		return fmt.Errorf("bitcoin data_dir validation failed: %s", msg)
	}
	return nil
}

func (s *Server) uninstallBitcoinCore(ctx context.Context) error {
	paths := bitcoinCoreAppPaths()
	if fileExists(paths.ComposePath) {
		_ = runCompose(ctx, paths.Root, paths.ComposePath, "down", "--remove-orphans")
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
	if _, err := ensureFileWithChange(paths.ComposePath, bitcoinCoreComposeContents(paths)); err != nil {
		return err
	}
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
		return err
	}
	if _, changed, err := syncBitcoinCoreRPCAllowList(ctx, paths); err != nil {
		return err
	} else if changed {
		if err := runCompose(ctx, paths.Root, paths.ComposePath, "restart", "bitcoind"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) stopBitcoinCore(ctx context.Context) error {
	paths := bitcoinCoreAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("Bitcoin Core is not installed")
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "stop")
}

func bitcoinCoreComposeContents(paths bitcoinCorePaths) string {
	return fmt.Sprintf(`services:
  bitcoind:
    image: %s
    user: "0:0"
    restart: unless-stopped
    entrypoint: ["/bin/sh", "/lightningos-storage-guard.sh"]
    command: ["bitcoind"]
    ports:
      - "8333:8333"
      - "127.0.0.1:8332:8332"
      - "127.0.0.1:28332:28332"
      - "127.0.0.1:28333:28333"
    volumes:
      - %s:/home/bitcoin/.bitcoin
      - %s:/lightningos-storage-guard.sh:ro
      - %s:/lightningos-expected-storage-id:ro
`, bitcoinCoreImage, paths.DataDir, paths.StorageGuardPath, paths.StorageIDPath)
}

func ensureBitcoinCoreStorageGuard(ctx context.Context, paths bitcoinCorePaths) error {
	if _, err := ensureFileWithChange(paths.StorageGuardPath, bitcoinCoreStorageGuardContents()); err != nil {
		return fmt.Errorf("failed to write Bitcoin Core storage guard: %w", err)
	}
	if handled, err := system.EnsureBitcoinCoreStorageWithBroker(ctx, paths.DataDir); handled {
		if err != nil {
			return fmt.Errorf("bitcoin storage enrollment failed: %w", err)
		}
		return nil
	}
	return errors.New("Bitcoin Core storage enrollment requires privileged broker enforce mode")
}

func bitcoinCoreStorageGuardContents() string {
	return `#!/bin/sh
set -eu

expected="$(tr -d '\r\n' < /lightningos-expected-storage-id)"
actual="$(tr -d '\r\n' < /home/bitcoin/.bitcoin/.lightningos-storage-id 2>/dev/null || true)"

if [ -z "$expected" ] || [ "$actual" != "$expected" ]; then
  echo "LightningOS storage guard: the configured Bitcoin data volume is missing or has the wrong identity; refusing to start bitcoind" >&2
  exit 78
fi

exec /entrypoint.sh "$@"
`
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
	if handled, err := system.EnsureBitcoinCoreConfigWithBroker(ctx, paths.DataDir, content); handled {
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
	password, err := randomToken(32)
	if err != nil {
		return "", err
	}
	lines := []string{
		"server=1",
		"printtoconsole=1",
		"txindex=1",
		"rpcuser=lightningos",
		"rpcpassword=" + password,
		"rpcbind=0.0.0.0:8332",
		"rpcallowip=127.0.0.1",
		"rpcallowip=172.17.0.0/16",
		"zmqpubrawblock=tcp://0.0.0.0:28332",
		"zmqpubrawtx=tcp://0.0.0.0:28333",
		"",
	}
	return strings.Join(lines, "\n"), nil
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
