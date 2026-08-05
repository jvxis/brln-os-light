package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"lightningos-light/internal/system"
)

type bitcoinCorePaths struct {
	Root             string
	DataDir          string
	ConfigPath       string
	SeedConfigPath   string
	ComposePath      string
	StorageIDPath    string
	StorageGuardPath string
}

type bitcoinCoreApp struct {
	server *Server
}

const (
	bitcoinCoreAppID            = "bitcoincore"
	bitcoinCoreImage            = "bitcoin/bitcoin:latest"
	bitcoinCoreDefaultDataDir   = "/data/bitcoin"
	bitcoinCoreDataDirStateFile = "data_dir"
	bitcoinCoreStorageIDFile    = "storage_id"
	bitcoinCoreStorageMarker    = ".lightningos-storage-id"
	bitcoinCoreStorageGuardFile = "storage-guard.sh"
	bitcoinCoreMinFreeKiB       = 10 * 1024 * 1024
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
		SeedConfigPath:   filepath.Join(root, "bitcoin.conf"),
		ComposePath:      filepath.Join(root, "docker-compose.yaml"),
		StorageIDPath:    filepath.Join(bitcoinCoreAppDataDir(), bitcoinCoreStorageIDFile),
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
	trimmed := strings.TrimSpace(dataDir)
	if trimmed == "" {
		return bitcoinCoreDefaultDataDir, nil
	}
	if strings.Contains(trimmed, "\\") {
		return "", errors.New("bitcoin data_dir must be a Linux absolute path")
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "", errors.New("bitcoin data_dir must be an absolute path")
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == "/" {
		return "", errors.New("bitcoin data_dir cannot be the filesystem root")
	}
	if cleaned == "/data" {
		return "", errors.New("bitcoin data_dir cannot be /data")
	}
	if !linuxPathHasSafeChars(cleaned) {
		return "", errors.New("bitcoin data_dir may only contain letters, numbers, slash, dot, underscore, and hyphen")
	}
	if cleaned == bitcoinCoreDefaultDataDir {
		return cleaned, nil
	}
	for _, blocked := range bitcoinCoreBlockedDataDirPrefixes() {
		if cleaned == blocked || strings.HasPrefix(cleaned, blocked+"/") {
			return "", fmt.Errorf("bitcoin data_dir cannot be inside %s", blocked)
		}
	}
	return cleaned, nil
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

	if err := ensureDocker(ctx); err != nil {
		return err
	}
	if err := ensureBitcoinCoreImage(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.Root, 0750); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}
	if err := ensureBitcoinCoreSeedConfig(paths); err != nil {
		return err
	}
	if err := syncBitcoinCoreConfig(ctx, paths); err != nil {
		return err
	}
	if err := ensureBitcoinCoreStorageGuard(ctx, paths); err != nil {
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
	if err := ensureBitcoinCoreSeedConfig(paths); err != nil {
		return err
	}
	if err := syncBitcoinCoreConfig(ctx, paths); err != nil {
		return err
	}
	if err := ensureBitcoinCoreStorageGuard(ctx, paths); err != nil {
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
	if err := os.MkdirAll(bitcoinCoreAppDataDir(), 0750); err != nil {
		return fmt.Errorf("failed to create Bitcoin Core app data directory: %w", err)
	}
	storageID := ""
	if raw, err := os.ReadFile(paths.StorageIDPath); err == nil {
		storageID = strings.TrimSpace(string(raw))
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to read Bitcoin Core storage identity: %w", err)
	}
	if storageID == "" {
		generated, err := randomToken(24)
		if err != nil {
			return fmt.Errorf("failed to generate Bitcoin Core storage identity: %w", err)
		}
		storageID = generated
		if err := writeFile(paths.StorageIDPath, storageID+"\n", 0600); err != nil {
			return fmt.Errorf("failed to persist Bitcoin Core storage identity: %w", err)
		}
	}

	if _, err := ensureFileWithChange(paths.StorageGuardPath, bitcoinCoreStorageGuardContents()); err != nil {
		return fmt.Errorf("failed to write Bitcoin Core storage guard: %w", err)
	}

	markerPath := filepath.Join(paths.DataDir, bitcoinCoreStorageMarker)
	cmd := "cp /lightningos-expected-storage-id /home/bitcoin/.bitcoin/" + bitcoinCoreStorageMarker +
		" && chmod 640 /home/bitcoin/.bitcoin/" + bitcoinCoreStorageMarker
	out, err := system.RunCommandWithSudo(
		ctx,
		"docker",
		"run",
		"--rm",
		"--entrypoint",
		"sh",
		"--user",
		"0:0",
		"-v",
		fmt.Sprintf("%s:/home/bitcoin/.bitcoin", paths.DataDir),
		"-v",
		fmt.Sprintf("%s:/lightningos-expected-storage-id:ro", paths.StorageIDPath),
		bitcoinCoreImage,
		"-c",
		cmd,
	)
	if err != nil {
		msg := strings.TrimSpace(out)
		if msg == "" {
			return fmt.Errorf("failed to mark Bitcoin Core storage at %s: %w", markerPath, err)
		}
		return fmt.Errorf("failed to mark Bitcoin Core storage at %s: %s", markerPath, msg)
	}
	if paths.DataDir != bitcoinCoreDefaultDataDir {
		if err := validateBitcoinCoreInstallDataDir(ctx, paths.DataDir); err != nil {
			cleanupOut, cleanupErr := system.RunCommandWithSudo(
				ctx,
				"docker",
				"run",
				"--rm",
				"--entrypoint",
				"rm",
				"--user",
				"0:0",
				"-v",
				fmt.Sprintf("%s:/home/bitcoin/.bitcoin", paths.DataDir),
				bitcoinCoreImage,
				"-f",
				"/home/bitcoin/.bitcoin/"+bitcoinCoreStorageMarker,
			)
			if cleanupErr != nil {
				cleanupMsg := strings.TrimSpace(cleanupOut)
				if cleanupMsg == "" {
					cleanupMsg = cleanupErr.Error()
				}
				return fmt.Errorf("bitcoin storage became unavailable while enabling its startup guard: %w; failed to remove the fallback marker: %s", err, cleanupMsg)
			}
			return fmt.Errorf("bitcoin storage became unavailable while enabling its startup guard: %w", err)
		}
	}
	return nil
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

func ensureBitcoinCoreSeedConfig(paths bitcoinCorePaths) error {
	info, err := os.Stat(paths.SeedConfigPath)
	if err == nil {
		if info.IsDir() {
			return fmt.Errorf("%s is a directory", paths.SeedConfigPath)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("failed to stat %s: %w", paths.SeedConfigPath, err)
	}
	content, err := defaultBitcoinCoreConfig()
	if err != nil {
		return err
	}
	return writeFile(paths.SeedConfigPath, content, 0640)
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

func syncBitcoinCoreConfig(ctx context.Context, paths bitcoinCorePaths) error {
	if !fileExists(paths.SeedConfigPath) {
		return fmt.Errorf("missing seed config %s", paths.SeedConfigPath)
	}
	if err := ensureBitcoinCoreImage(ctx); err != nil {
		return err
	}
	cmd := "if [ ! -f /home/bitcoin/.bitcoin/bitcoin.conf ]; then cp /tmp/bitcoin.conf /home/bitcoin/.bitcoin/bitcoin.conf; chmod 640 /home/bitcoin/.bitcoin/bitcoin.conf; fi"
	out, err := system.RunCommandWithSudo(
		ctx,
		"docker",
		"run",
		"--rm",
		"--entrypoint",
		"sh",
		"--user",
		"0:0",
		"-v",
		fmt.Sprintf("%s:/home/bitcoin/.bitcoin", paths.DataDir),
		"-v",
		fmt.Sprintf("%s:/tmp/bitcoin.conf:ro", paths.SeedConfigPath),
		bitcoinCoreImage,
		"-c",
		cmd,
	)
	if err != nil {
		msg := strings.TrimSpace(out)
		if msg == "" {
			return fmt.Errorf("failed to seed bitcoin.conf: %w", err)
		}
		return fmt.Errorf("failed to seed bitcoin.conf: %s", msg)
	}
	return nil
}

func ensureBitcoinCoreImage(ctx context.Context) error {
	if _, err := system.RunCommandWithSudo(ctx, "docker", "image", "inspect", bitcoinCoreImage); err == nil {
		return nil
	}
	out, err := system.RunCommandWithSudo(ctx, "docker", "pull", bitcoinCoreImage)
	if err != nil {
		msg := strings.TrimSpace(out)
		if msg == "" {
			return fmt.Errorf("failed to pull %s: %w", bitcoinCoreImage, err)
		}
		return fmt.Errorf("failed to pull %s: %s", bitcoinCoreImage, msg)
	}
	return nil
}
