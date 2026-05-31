package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"lightningos-light/internal/config"
)

const (
	elementsAppID            = "elements"
	elementsVersion          = "23.3.1"
	elementsUser             = "losop"
	elementsServiceName      = "lightningos-elements"
	elementsRPCPort          = 7041
	elementsFallbackFee      = "0.00001"
	elementsDefaultDataDir   = "/data/elements"
	elementsDataDirStateFile = "data_dir"
	elementsMinFreeKiB       = 1024 * 1024
)

var elementsAssetDirs = []string{
	"02f22f8d9c76ab41661a2729e4752e2c5d1a263012141b86ea98af5472df5189:DePix",
	"ce091c998b83c78bb71a632313ba3760f1763d9cfcffae02258ffa9865a37bd2:USDT",
}

type elementsPaths struct {
	Root                string
	DataDir             string
	BinDir              string
	AppDataDir          string
	ElementsdPath       string
	ElementsCliPath     string
	ConfigPath          string
	ServicePath         string
	VersionPath         string
	RPCCredsPath        string
	MainchainSourcePath string
}

type elementsApp struct {
	server *Server
}

type elementsConfigValues struct {
	RPCUser       string
	RPCPass       string
	MainchainHost string
	MainchainPort int
	MainchainUser string
	MainchainPass string
}

type elementsInstallOptions struct {
	DataDir      string `json:"data_dir"`
	StorageMount string `json:"storage_mount"`
}

func newElementsApp(s *Server) appHandler {
	return elementsApp{server: s}
}

func elementsDefinition() appDefinition {
	return appDefinition{
		ID:          elementsAppID,
		Name:        "Elements",
		Description: "Run a Liquid Elements node (native binary).",
		Port:        0,
	}
}

func (a elementsApp) Definition() appDefinition {
	return elementsDefinition()
}

func (a elementsApp) Info(ctx context.Context) (appInfo, error) {
	def := a.Definition()
	info := newAppInfo(def)
	paths := elementsAppPaths()
	if !fileExists(paths.ElementsdPath) {
		return info, nil
	}
	info.Installed = true
	status, err := elementsServiceStatus(ctx)
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func (a elementsApp) Install(ctx context.Context) error {
	return a.server.installElements(ctx)
}

func (a elementsApp) Uninstall(ctx context.Context) error {
	return a.server.uninstallElements(ctx)
}

func (a elementsApp) Start(ctx context.Context) error {
	return a.server.startElements(ctx)
}

func (a elementsApp) Stop(ctx context.Context) error {
	return a.server.stopElements(ctx)
}

func elementsAppPaths() elementsPaths {
	root := filepath.Join(appsRoot, elementsAppID)
	dataDir := readElementsDataDir()
	binDir := filepath.Join(root, "bin")
	appDataDir := elementsAppDataDir()
	return elementsPaths{
		Root:                root,
		DataDir:             dataDir,
		BinDir:              binDir,
		AppDataDir:          appDataDir,
		ElementsdPath:       filepath.Join(binDir, "elementsd"),
		ElementsCliPath:     filepath.Join(binDir, "elements-cli"),
		ConfigPath:          filepath.Join(dataDir, "elements.conf"),
		ServicePath:         filepath.Join("/etc/systemd/system", elementsServiceName+".service"),
		VersionPath:         filepath.Join(root, "VERSION"),
		RPCCredsPath:        filepath.Join(appDataDir, "rpc.env"),
		MainchainSourcePath: filepath.Join(appDataDir, "mainchain_source"),
	}
}

func elementsAppDataDir() string {
	return filepath.Join(appsDataRoot, elementsAppID)
}

func elementsDataDirStatePath() string {
	return filepath.Join(elementsAppDataDir(), elementsDataDirStateFile)
}

func readElementsDataDir() string {
	raw, err := os.ReadFile(elementsDataDirStatePath())
	if err != nil {
		return elementsDefaultDataDir
	}
	normalized, err := normalizeElementsDataDir(string(raw))
	if err != nil {
		return elementsDefaultDataDir
	}
	return normalized
}

func writeElementsDataDir(dataDir string) error {
	normalized, err := normalizeElementsDataDir(dataDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(elementsAppDataDir(), 0750); err != nil {
		return err
	}
	return writeFile(elementsDataDirStatePath(), normalized+"\n", 0640)
}

func normalizeElementsDataDir(dataDir string) (string, error) {
	trimmed := strings.TrimSpace(dataDir)
	if trimmed == "" {
		return elementsDefaultDataDir, nil
	}
	if strings.Contains(trimmed, "\\") {
		return "", errors.New("elements data_dir must be a Linux absolute path")
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "", errors.New("elements data_dir must be an absolute path")
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == "/" {
		return "", errors.New("elements data_dir cannot be the filesystem root")
	}
	if !linuxPathHasSafeChars(cleaned) {
		return "", errors.New("elements data_dir may only contain letters, numbers, slash, dot, underscore, and hyphen")
	}
	for _, blocked := range elementsBlockedDataDirPrefixes() {
		if cleaned == blocked || strings.HasPrefix(cleaned, blocked+"/") {
			return "", fmt.Errorf("elements data_dir cannot be inside %s", blocked)
		}
	}
	return cleaned, nil
}

func elementsBlockedDataDirPrefixes() []string {
	return []string{
		"/bin",
		"/boot",
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
		"/data/bitcoin",
		"/data/lnd",
	}
}

func (s *Server) installElements(ctx context.Context) error {
	return s.installElementsWithOptions(ctx, elementsInstallOptions{})
}

func (s *Server) installElementsWithOptions(ctx context.Context, opts elementsInstallOptions) error {
	requestedDataDir, dataDirRequested, err := resolveElementsInstallDataDir(ctx, opts)
	if err != nil {
		return err
	}
	if dataDirRequested {
		normalized, err := normalizeElementsDataDir(requestedDataDir)
		if err != nil {
			return err
		}
		currentPaths := elementsAppPaths()
		if fileExists(currentPaths.ElementsdPath) && normalized != currentPaths.DataDir {
			return errors.New("Elements data directory cannot be changed after installation")
		}
		if normalized == elementsDefaultDataDir {
			if err := writeElementsDataDir(normalized); err != nil {
				return err
			}
		} else {
			if err := validateElementsInstallDataDir(ctx, normalized); err != nil {
				return err
			}
			if err := writeElementsDataDir(normalized); err != nil {
				return err
			}
		}
	}

	paths := elementsAppPaths()
	if err := os.MkdirAll(paths.Root, 0750); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}
	if err := os.MkdirAll(paths.AppDataDir, 0750); err != nil {
		return fmt.Errorf("failed to create app data directory: %w", err)
	}
	if err := ensureElementsDataDir(ctx, paths); err != nil {
		return err
	}
	if err := ensureElementsBinary(ctx, paths); err != nil {
		return err
	}
	if err := ensureElementsConfig(ctx, paths, s.cfg); err != nil {
		return err
	}
	if err := ensureElementsService(ctx, paths); err != nil {
		return err
	}
	if _, err := runSystemd(ctx, "systemctl", "enable", "--now", elementsServiceName); err != nil {
		return err
	}
	return nil
}

func resolveElementsInstallDataDir(ctx context.Context, opts elementsInstallOptions) (string, bool, error) {
	requestedDataDir := strings.TrimSpace(opts.DataDir)
	requestedMount := strings.TrimSpace(opts.StorageMount)
	if requestedDataDir != "" && requestedMount != "" {
		return "", false, errors.New("choose either data_dir or storage_mount for Elements installation")
	}
	if requestedMount != "" {
		dataDir, err := resolveInstallDataDirFromStorageMount(ctx, elementsAppID, requestedMount)
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

func validateElementsInstallDataDir(ctx context.Context, dataDir string) error {
	normalized, err := normalizeElementsDataDir(dataDir)
	if err != nil {
		return err
	}
	parent := path.Dir(normalized)
	script := fmt.Sprintf(`set -e
parent=%s
data=%s
user=%s
if ! id "$user" >/dev/null 2>&1; then
  echo "service user does not exist: $user" >&2
  exit 11
fi
nearest="$parent"
while [ ! -e "$nearest" ] && [ "$nearest" != "/" ]; do
  nearest="$(dirname "$nearest")"
done
if [ ! -d "$nearest" ]; then
  echo "nearest existing parent is not a directory: $nearest" >&2
  exit 12
fi
if command -v findmnt >/dev/null 2>&1; then
  mount_target="$(findmnt -T "$nearest" -no TARGET 2>/dev/null | head -n1 || true)"
  if [ -z "$mount_target" ]; then
    echo "parent directory is not on a mounted filesystem: $nearest" >&2
    exit 13
  fi
  if [ "$mount_target" = "/" ]; then
    echo "parent directory is on the root filesystem; mount the target volume first: $parent" >&2
    exit 14
  fi
fi
mkdir -p "$parent"
mkdir -p "$data"
if [ ! -d "$data" ]; then
  echo "data directory is not a directory: $data" >&2
  exit 15
fi
grant_traverse() {
  dir="$1"
  if command -v setfacl >/dev/null 2>&1; then
    setfacl -m "u:$user:x" "$dir" 2>/dev/null || chmod o+x "$dir"
  else
    chmod o+x "$dir"
  fi
}
current=""
rest="${parent#/}"
old_ifs="$IFS"
IFS="/"
for part in $rest; do
  current="$current/$part"
  if [ -d "$current" ]; then
    grant_traverse "$current"
  fi
done
IFS="$old_ifs"
chown "$user:$user" "$data"
chmod 750 "$data"
if ! command -v runuser >/dev/null 2>&1; then
  echo "runuser is required to validate $user access" >&2
  exit 16
fi
runuser -u "$user" -- test -x "$parent"
runuser -u "$user" -- /bin/sh -c 'set -e
data="$1"
test -d "$data"
test -r "$data"
test -w "$data"
touch "$data/.lightningos-user-write-test"
rm -f "$data/.lightningos-user-write-test"
' sh "$data"
available="$(df -Pk "$data" | awk 'NR==2 {print $4}')"
if [ -n "$available" ] && [ "$available" -lt %d ]; then
  echo "not enough free space in $data" >&2
  exit 17
fi
`, shellQuote(parent), shellQuote(normalized), shellQuote(elementsUser), elementsMinFreeKiB)
	out, err := runSystemd(ctx, "/bin/sh", "-c", script)
	if err != nil {
		msg := strings.TrimSpace(out)
		if msg == "" {
			return fmt.Errorf("elements data_dir validation failed: %w", err)
		}
		return fmt.Errorf("elements data_dir validation failed: %s", msg)
	}
	return nil
}

func (s *Server) startElements(ctx context.Context) error {
	paths := elementsAppPaths()
	if !fileExists(paths.ElementsdPath) {
		return errors.New("Elements is not installed")
	}
	if err := ensureElementsDataDir(ctx, paths); err != nil {
		return err
	}
	if err := ensureElementsConfig(ctx, paths, s.cfg); err != nil {
		return err
	}
	if err := ensureElementsService(ctx, paths); err != nil {
		return err
	}
	if _, err := runSystemd(ctx, "systemctl", "restart", elementsServiceName); err != nil {
		return err
	}
	return nil
}

func (s *Server) stopElements(ctx context.Context) error {
	paths := elementsAppPaths()
	if !fileExists(paths.ElementsdPath) {
		return errors.New("Elements is not installed")
	}
	if _, err := runSystemd(ctx, "systemctl", "stop", elementsServiceName); err != nil {
		return err
	}
	return nil
}

func (s *Server) uninstallElements(ctx context.Context) error {
	paths := elementsAppPaths()
	_, _ = runSystemd(ctx, "systemctl", "disable", "--now", elementsServiceName)
	_, _ = runSystemd(ctx, "/bin/sh", "-c", "rm -f "+paths.ServicePath+" /etc/systemd/system/elementsd.service /etc/systemd/system/multi-user.target.wants/"+elementsServiceName+".service /etc/systemd/system/multi-user.target.wants/elementsd.service")
	_, _ = runSystemd(ctx, "systemctl", "daemon-reload")
	if _, err := runSystemd(ctx, "/bin/sh", "-c", "rm -rf "+paths.Root); err != nil {
		return fmt.Errorf("failed to remove app files: %w", err)
	}
	if _, err := runSystemd(ctx, "/bin/sh", "-c", "rm -rf "+paths.AppDataDir); err != nil {
		return fmt.Errorf("failed to remove app data: %w", err)
	}
	return nil
}

func ensureElementsDataDir(ctx context.Context, paths elementsPaths) error {
	script := fmt.Sprintf(`set -e
data=%s
user=%s
default_data=%s
parent="$(dirname "$data")"
if [ ! -d "$parent" ]; then
  if [ "$data" = "$default_data" ]; then
    mkdir -p "$parent"
    chmod 755 "$parent"
  else
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
        exit 12
      fi
      if [ "$mount_target" = "/" ]; then
        echo "parent directory is on the root filesystem; mount the target volume first: $parent" >&2
        exit 13
      fi
    fi
    mkdir -p "$parent"
  fi
fi
if ! id "$user" >/dev/null 2>&1; then
  echo "service user does not exist: $user" >&2
  exit 11
fi
if [ "$data" != "$default_data" ] && command -v findmnt >/dev/null 2>&1; then
  mount_target="$(findmnt -T "$parent" -no TARGET 2>/dev/null | head -n1 || true)"
  if [ -z "$mount_target" ]; then
    echo "parent directory is not on a mounted filesystem: $parent" >&2
    exit 12
  fi
  if [ "$mount_target" = "/" ]; then
    echo "parent directory is on the root filesystem; mount the target volume first: $parent" >&2
    exit 13
  fi
fi
grant_traverse() {
  dir="$1"
  if command -v setfacl >/dev/null 2>&1; then
    setfacl -m "u:$user:x" "$dir" 2>/dev/null || chmod o+x "$dir"
  else
    chmod o+x "$dir"
  fi
}
current=""
rest="${parent#/}"
old_ifs="$IFS"
IFS="/"
for part in $rest; do
  current="$current/$part"
  if [ -d "$current" ]; then
    grant_traverse "$current"
  fi
done
IFS="$old_ifs"
mkdir -p "$data"
chown "$user:$user" "$data"
chmod 750 "$data"
if ! command -v runuser >/dev/null 2>&1; then
  echo "runuser is required to validate $user access" >&2
  exit 14
fi
runuser -u "$user" -- test -x "$parent"
runuser -u "$user" -- /bin/sh -c 'set -e
data="$1"
test -d "$data"
test -r "$data"
test -w "$data"
touch "$data/.lightningos-user-write-test"
rm -f "$data/.lightningos-user-write-test"
' sh "$data"
`, shellQuote(paths.DataDir), shellQuote(elementsUser), shellQuote(elementsDefaultDataDir))
	if _, err := runSystemd(ctx, "/bin/sh", "-c", script); err != nil {
		return fmt.Errorf("failed to prepare %s: %w", paths.DataDir, err)
	}
	link := fmt.Sprintf(`
if [ -d "/home/%[1]s" ]; then
  if [ -L "/home/%[1]s/.elements" ]; then
    target="$(readlink "/home/%[1]s/.elements" || true)"
    if [ "$target" != "%[2]s" ]; then
      ln -sf "%[2]s" "/home/%[1]s/.elements"
    fi
  elif [ ! -e "/home/%[1]s/.elements" ]; then
    ln -s "%[2]s" "/home/%[1]s/.elements"
  fi
  chown -h %[1]s:%[1]s "/home/%[1]s/.elements" 2>/dev/null || true
fi
`, elementsUser, paths.DataDir)
	_, _ = runSystemd(ctx, "/bin/sh", "-c", link)
	return nil
}

func ensureElementsBinary(ctx context.Context, paths elementsPaths) error {
	if readSecretFile(paths.VersionPath) == elementsVersion && fileExists(paths.ElementsdPath) && fileExists(paths.ElementsCliPath) {
		return nil
	}
	arch, err := elementsArchiveSuffix()
	if err != nil {
		return err
	}
	script := fmt.Sprintf(`set -e
version=%s
archive=elements-$version-%s.tar.gz
base=https://github.com/ElementsProject/elements/releases/download/elements-$version
tmp="$(mktemp -d)"
cleanup() { rm -rf "$tmp"; }
trap cleanup EXIT
mkdir -p "%s"
curl -fsSL "$base/$archive" -o "$tmp/$archive"
curl -fsSL "$base/SHA256SUMS.asc" -o "$tmp/SHA256SUMS.asc"
cd "$tmp"
sha256sum --ignore-missing --check SHA256SUMS.asc
tar -xzf "$archive"
install -m 0755 "$tmp/elements-$version/bin/elementsd" "%s"
install -m 0755 "$tmp/elements-$version/bin/elements-cli" "%s"
chown %s:%s "%s" "%s"
`, elementsVersion, arch, paths.BinDir, paths.ElementsdPath, paths.ElementsCliPath, elementsUser, elementsUser, paths.ElementsdPath, paths.ElementsCliPath)
	if _, err := runSystemd(ctx, "/bin/sh", "-c", script); err != nil {
		return err
	}
	return writeFile(paths.VersionPath, elementsVersion+"\n", 0640)
}

func ensureElementsConfig(ctx context.Context, paths elementsPaths, cfg *config.Config) error {
	if cfg == nil {
		return errors.New("config unavailable")
	}
	if err := os.MkdirAll(paths.AppDataDir, 0750); err != nil {
		return fmt.Errorf("failed to create app data directory: %w", err)
	}
	rpcUser, rpcPass, err := ensureElementsCredentials(paths)
	if err != nil {
		return err
	}
	mainchain, err := resolveElementsMainchainConfig(ctx, cfg, paths)
	if err != nil {
		return err
	}
	if storedSource, sourceSet := readElementsMainchainSourceState(paths); !sourceSet || storedSource != mainchain.Source {
		if err := writeElementsMainchainSource(paths, mainchain.Source); err != nil {
			return err
		}
	}
	values := elementsConfigValues{
		RPCUser:       rpcUser,
		RPCPass:       rpcPass,
		MainchainHost: mainchain.Host,
		MainchainPort: mainchain.Port,
		MainchainUser: mainchain.User,
		MainchainPass: mainchain.Pass,
	}
	raw, err := readElementsConfig(ctx, paths)
	if err != nil {
		return err
	}
	updated := raw
	if raw == "" {
		updated = defaultElementsConfig(values)
	} else {
		updated = updateElementsConfig(raw, values)
	}
	if updated == raw {
		return nil
	}
	return writeElementsConfig(ctx, paths, updated)
}

func ensureElementsService(ctx context.Context, paths elementsPaths) error {
	content := elementsServiceContents(paths)
	if existing, err := os.ReadFile(paths.ServicePath); err == nil && string(existing) == content {
		return nil
	}
	tmpPath := filepath.Join(paths.Root, "elements.service.tmp")
	if err := writeFile(tmpPath, content, 0644); err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	script := fmt.Sprintf("install -m 0644 %s %s", tmpPath, paths.ServicePath)
	if _, err := runSystemd(ctx, "/bin/sh", "-c", script); err != nil {
		return err
	}
	if _, err := runSystemd(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	return nil
}

func ensureElementsCredentials(paths elementsPaths) (string, string, error) {
	if fileExists(paths.RPCCredsPath) {
		content, err := os.ReadFile(paths.RPCCredsPath)
		if err == nil {
			user, pass := parseElementsCredentials(string(content))
			if user != "" && pass != "" {
				return user, pass, nil
			}
		}
	}
	password, err := randomToken(24)
	if err != nil {
		return "", "", err
	}
	user := "elements"
	content := fmt.Sprintf("RPC_USER=%s\nRPC_PASS=%s\n", user, password)
	if err := writeFile(paths.RPCCredsPath, content, 0600); err != nil {
		return "", "", err
	}
	return user, password, nil
}

func parseElementsCredentials(content string) (string, string) {
	var user string
	var pass string
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "RPC_USER=") {
			user = strings.TrimSpace(strings.TrimPrefix(line, "RPC_USER="))
		}
		if strings.HasPrefix(line, "RPC_PASS=") {
			pass = strings.TrimSpace(strings.TrimPrefix(line, "RPC_PASS="))
		}
	}
	return user, pass
}

func defaultElementsConfig(values elementsConfigValues) string {
	lines := []string{
		"# LightningOS Elements configuration",
		"chain=liquidv1",
		"daemon=0",
		"server=1",
		"listen=1",
		"txindex=1",
		"validatepegin=1",
		"dbcache=300",
		"maxmempool=50",
		"maxconnections=40",
		"par=2",
		"trim_headers=1",
		"",
		"# Asset registry entries (guide defaults)",
		"assetdir=" + elementsAssetDirs[0],
		"assetdir=" + elementsAssetDirs[1],
		"",
		"# Elements RPC (local)",
		"rpcuser=" + values.RPCUser,
		"rpcpassword=" + values.RPCPass,
		"rpcport=" + strconv.Itoa(elementsRPCPort),
		"rpcbind=127.0.0.1",
		"rpcallowip=127.0.0.1",
		"",
		"# Mainchain RPC (Bitcoin remote)",
		"mainchainrpchost=" + values.MainchainHost,
		"mainchainrpcport=" + strconv.Itoa(values.MainchainPort),
		"mainchainrpcuser=" + values.MainchainUser,
		"mainchainrpcpassword=" + values.MainchainPass,
		"",
		"fallbackfee=" + elementsFallbackFee,
		"",
	}
	return strings.Join(lines, "\n")
}

func updateElementsConfig(raw string, values elementsConfigValues) string {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(strings.TrimRight(normalized, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = []string{}
	}
	force := map[string]string{
		"chain":                "liquidv1",
		"daemon":               "0",
		"server":               "1",
		"listen":               "1",
		"txindex":              "1",
		"validatepegin":        "1",
		"dbcache":              "300",
		"maxmempool":           "50",
		"maxconnections":       "40",
		"par":                  "2",
		"trim_headers":         "1",
		"rpcuser":              values.RPCUser,
		"rpcpassword":          values.RPCPass,
		"rpcport":              strconv.Itoa(elementsRPCPort),
		"mainchainrpchost":     values.MainchainHost,
		"mainchainrpcport":     strconv.Itoa(values.MainchainPort),
		"mainchainrpcuser":     values.MainchainUser,
		"mainchainrpcpassword": values.MainchainPass,
		"fallbackfee":          elementsFallbackFee,
	}
	optional := map[string]string{
		"rpcbind": "127.0.0.1",
	}
	seen := map[string]bool{}
	assetSeen := map[string]bool{}
	allowSeen := map[string]bool{}
	updated := []string{}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			updated = append(updated, line)
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			updated = append(updated, line)
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "assetdir" {
			if value != "" {
				assetSeen[value] = true
			}
			updated = append(updated, line)
			continue
		}
		if key == "rpcallowip" {
			if value != "" {
				allowSeen[value] = true
			}
			updated = append(updated, line)
			continue
		}
		if forced, ok := force[key]; ok {
			updated = append(updated, key+"="+forced)
			seen[key] = true
			continue
		}
		if _, ok := optional[key]; ok {
			updated = append(updated, line)
			seen[key] = true
			continue
		}
		updated = append(updated, line)
	}

	for key, value := range force {
		if !seen[key] {
			updated = append(updated, key+"="+value)
		}
	}
	for key, value := range optional {
		if !seen[key] {
			updated = append(updated, key+"="+value)
		}
	}
	for _, asset := range elementsAssetDirs {
		if !assetSeen[asset] {
			updated = append(updated, "assetdir="+asset)
		}
	}
	if !allowSeen["127.0.0.1"] {
		updated = append(updated, "rpcallowip=127.0.0.1")
	}

	return strings.Join(updated, "\n") + "\n"
}

func writeElementsConfig(ctx context.Context, paths elementsPaths, content string) error {
	tmpPath := filepath.Join(paths.Root, "elements.conf.tmp")
	if err := writeFile(tmpPath, content, 0640); err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	script := fmt.Sprintf("install -m 0600 -o %s -g %s %s %s", elementsUser, elementsUser, tmpPath, paths.ConfigPath)
	if _, err := runSystemd(ctx, "/bin/sh", "-c", script); err != nil {
		return err
	}
	return nil
}

func readElementsConfig(ctx context.Context, paths elementsPaths) (string, error) {
	out, err := runSystemd(ctx, "/bin/sh", "-c", "cat "+paths.ConfigPath)
	if err != nil {
		msg := strings.ToLower(out)
		if strings.Contains(msg, "no such file") || strings.Contains(strings.ToLower(err.Error()), "no such file") {
			return "", nil
		}
		return "", err
	}
	return strings.TrimRight(out, "\n") + "\n", nil
}

func elementsServiceContents(paths elementsPaths) string {
	return fmt.Sprintf(`[Unit]
Description=LightningOS Elements (Liquid)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=%s
Group=%s
ExecStart=%s -datadir=%s -conf=%s
Restart=on-failure
RestartSec=3
TimeoutStartSec=infinity
TimeoutStopSec=600
PrivateTmp=true
ProtectSystem=full
NoNewPrivileges=true
PrivateDevices=true
MemoryDenyWriteExecute=true
ReadWritePaths=%s

[Install]
WantedBy=multi-user.target
Alias=elementsd.service
`, elementsUser, elementsUser, paths.ElementsdPath, paths.DataDir, paths.ConfigPath, paths.DataDir)
}

func elementsArchiveSuffix() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64-linux-gnu", nil
	case "arm64":
		return "aarch64-linux-gnu", nil
	default:
		return "", fmt.Errorf("unsupported architecture for Elements: %s", runtime.GOARCH)
	}
}

func parseMainchainRPC(host string) (string, int) {
	return normalizeBitcoinRPCHostPort(host, 8332)
}

type elementsMainchainConfig struct {
	Source string
	Host   string
	Port   int
	User   string
	Pass   string
}

func resolveElementsMainchainConfig(ctx context.Context, cfg *config.Config, paths elementsPaths) (elementsMainchainConfig, error) {
	source, sourceSet := readElementsMainchainSourceState(paths)
	if sourceSet {
		return resolveElementsMainchainSourceConfig(ctx, cfg, source)
	}

	if readBitcoinSource() == "local" {
		if localCfg, err := resolveElementsMainchainSourceConfig(ctx, cfg, "local"); err == nil {
			return localCfg, nil
		} else if remoteCfg, remoteErr := resolveElementsMainchainSourceConfig(ctx, cfg, "remote"); remoteErr == nil {
			return remoteCfg, nil
		} else {
			return elementsMainchainConfig{}, fmt.Errorf("%v; %v", err, remoteErr)
		}
	}

	if remoteCfg, err := resolveElementsMainchainSourceConfig(ctx, cfg, "remote"); err == nil {
		return remoteCfg, nil
	}
	if localCfg, err := resolveElementsMainchainSourceConfig(ctx, cfg, "local"); err == nil {
		return localCfg, nil
	}
	return elementsMainchainConfig{}, errors.New("bitcoin mainchain RPC credentials missing: configure Bitcoin remote or a local bitcoind RPC user/pass")
}

func resolveElementsMainchainSourceConfig(ctx context.Context, cfg *config.Config, source string) (elementsMainchainConfig, error) {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "local":
		return resolveElementsLocalMainchainConfig(ctx)
	case "remote":
		return resolveElementsRemoteMainchainConfig(cfg)
	default:
		return elementsMainchainConfig{}, errors.New("invalid mainchain source")
	}
}

func resolveElementsLocalMainchainConfig(ctx context.Context) (elementsMainchainConfig, error) {
	localCfg, err := resolveElementsLocalBitcoinRPCConfig(ctx)
	if err != nil {
		return elementsMainchainConfig{}, err
	}
	host, port := parseMainchainRPC(localCfg.Host)
	return elementsMainchainConfig{
		Source: "local",
		Host:   host,
		Port:   port,
		User:   localCfg.User,
		Pass:   localCfg.Pass,
	}, nil
}

func resolveElementsRemoteMainchainConfig(cfg *config.Config) (elementsMainchainConfig, error) {
	if cfg == nil {
		return elementsMainchainConfig{}, errors.New("config unavailable")
	}
	host, port := parseMainchainRPC(cfg.BitcoinRemote.RPCHost)
	mainUser, mainPass := readBitcoinSecrets()
	if mainUser == "" || mainPass == "" {
		return elementsMainchainConfig{}, errors.New("bitcoin remote RPC credentials missing")
	}
	return elementsMainchainConfig{
		Source: "remote",
		Host:   host,
		Port:   port,
		User:   mainUser,
		Pass:   mainPass,
	}, nil
}

func resolveElementsLocalBitcoinRPCConfig(ctx context.Context) (bitcoinRPCConfig, error) {
	if cfg, ok := readElementsLocalBitcoinRPCConfigFromLNDConf(); ok {
		return cfg, nil
	}
	if cfg, _, err := readBitcoinLocalRPCConfig(ctx); err == nil {
		if !isLocalRPCHost(cfg.Host) {
			return bitcoinRPCConfig{}, fmt.Errorf("local bitcoin RPC host is not local: %s", cfg.Host)
		}
		return cfg, nil
	} else {
		return bitcoinRPCConfig{}, fmt.Errorf("local bitcoin RPC credentials unavailable: configure bitcoind.rpcuser and bitcoind.rpcpass in %s, use a readable bitcoin.conf, or switch Elements to Bitcoin remote: %w", lndConfPath, err)
	}
}

func readElementsLocalBitcoinRPCConfigFromLNDConf() (bitcoinRPCConfig, bool) {
	raw, err := os.ReadFile(lndConfPath)
	if err != nil {
		return bitcoinRPCConfig{}, false
	}
	return parseElementsLocalBitcoinRPCConfigFromLNDConf(string(raw))
}

func parseElementsLocalBitcoinRPCConfigFromLNDConf(raw string) (bitcoinRPCConfig, bool) {
	if cfg, ok := parseBitcoindRPCConfigFromLNDConf(raw); ok && isLocalRPCHost(cfg.Host) {
		return cfg, true
	}
	if cfg, ok := parseBitcoinTaggedRPCConfigFromLNDConf(raw, "local"); ok && isLocalRPCHost(cfg.Host) {
		return cfg, true
	}
	return bitcoinRPCConfig{}, false
}

func readElementsMainchainSource(paths elementsPaths) string {
	source, _ := readElementsMainchainSourceState(paths)
	return source
}

func readElementsMainchainSourceState(paths elementsPaths) (string, bool) {
	raw, err := os.ReadFile(paths.MainchainSourcePath)
	if err != nil {
		return "remote", false
	}
	source := strings.ToLower(strings.TrimSpace(string(raw)))
	if source != "local" && source != "remote" {
		return "remote", false
	}
	return source, true
}

func writeElementsMainchainSource(paths elementsPaths, source string) error {
	normalized := strings.ToLower(strings.TrimSpace(source))
	if normalized != "local" && normalized != "remote" {
		return errors.New("invalid mainchain source")
	}
	if err := os.MkdirAll(filepath.Dir(paths.MainchainSourcePath), 0750); err != nil {
		return err
	}
	return writeFile(paths.MainchainSourcePath, normalized+"\n", 0640)
}

func readLocalBitcoinRPCConfigFromFile(ctx context.Context) (bitcoinRPCConfig, error) {
	paths := bitcoinCoreAppPaths()
	content, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		out, runErr := runSystemd(ctx, "/bin/sh", "-c", "cat "+paths.ConfigPath)
		if runErr != nil {
			return bitcoinRPCConfig{}, fmt.Errorf("failed to read local bitcoin.conf: %w", err)
		}
		content = []byte(out)
	}
	raw := string(content)
	user, pass, _, _ := parseBitcoinCoreRPCConfig(raw)
	if user == "" || pass == "" {
		return bitcoinRPCConfig{}, errors.New("local RPC credentials missing")
	}
	port := parseBitcoinRPCPort(raw)
	host := fmt.Sprintf("127.0.0.1:%d", port)
	return bitcoinRPCConfig{
		Host: host,
		User: user,
		Pass: pass,
	}, nil
}

func parseBitcoinRPCPort(raw string) int {
	port := 8332
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	for _, line := range strings.Split(normalized, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "rpcport" {
			parsed, err := strconv.Atoi(value)
			if err == nil && parsed > 0 && parsed < 65536 {
				return parsed
			}
		}
		if key == "rpcbind" {
			if strings.Contains(value, ":") {
				hostPart, portPart, err := net.SplitHostPort(value)
				if err == nil && hostPart != "" {
					parsed, err := strconv.Atoi(portPart)
					if err == nil && parsed > 0 && parsed < 65536 {
						port = parsed
					}
				}
			}
		}
	}
	return port
}

func parseElementsMainchainConfig(raw string) (string, int) {
	host := ""
	port := 0
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	for _, line := range strings.Split(normalized, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		switch key {
		case "mainchainrpchost":
			host = value
		case "mainchainrpcport":
			parsed, err := strconv.Atoi(value)
			if err == nil && parsed > 0 && parsed < 65536 {
				port = parsed
			}
		}
	}
	return host, port
}

func elementsServiceStatus(ctx context.Context) (string, error) {
	out, err := runSystemd(ctx, "systemctl", "is-active", elementsServiceName)
	if err != nil {
		state := strings.TrimSpace(out)
		if state == "activating" {
			return "running", nil
		}
		if state == "inactive" || state == "failed" || state == "deactivating" {
			return "stopped", nil
		}
		return "unknown", err
	}
	state := strings.TrimSpace(out)
	switch state {
	case "active", "activating":
		return "running", nil
	case "inactive", "failed", "deactivating":
		return "stopped", nil
	default:
		return "unknown", nil
	}
}
