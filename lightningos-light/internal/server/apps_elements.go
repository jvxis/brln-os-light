package server

import (
  "context"
  "errors"
  "fmt"
  "net"
  "os"
  "path/filepath"
  "runtime"
  "strconv"
  "strings"

  "lightningos-light/internal/config"
)

const (
  elementsAppID = "elements"
  elementsVersion = "23.3.1"
  elementsUser = "losop"
  elementsServiceName = "lightningos-elements"
  elementsRPCPort = 7041
  elementsFallbackFee = "0.00001"
)

var elementsAssetDirs = []string{
  "02f22f8d9c76ab41661a2729e4752e2c5d1a263012141b86ea98af5472df5189:DePix",
  "ce091c998b83c78bb71a632313ba3760f1763d9cfcffae02258ffa9865a37bd2:USDT",
}

type elementsPaths struct {
  Root string
  DataDir string
  BinDir string
  AppDataDir string
  ElementsdPath string
  ElementsCliPath string
  ConfigPath string
  ServicePath string
  VersionPath string
  RPCCredsPath string
  MainchainSourcePath string
}

type elementsApp struct {
  server *Server
}

type elementsConfigValues struct {
  RPCUser string
  RPCPass string
  MainchainHost string
  MainchainPort int
  MainchainUser string
  MainchainPass string
}

func newElementsApp(s *Server) appHandler {
  return elementsApp{server: s}
}

func elementsDefinition() appDefinition {
  return appDefinition{
    ID: elementsAppID,
    Name: "Elements",
    Description: "Run a Liquid Elements node (native binary).",
    Port: 0,
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
  dataDir := "/data/elements"
  binDir := filepath.Join(root, "bin")
  appDataDir := filepath.Join(appsDataRoot, elementsAppID)
  return elementsPaths{
    Root: root,
    DataDir: dataDir,
    BinDir: binDir,
    AppDataDir: appDataDir,
    ElementsdPath: filepath.Join(binDir, "elementsd"),
    ElementsCliPath: filepath.Join(binDir, "elements-cli"),
    ConfigPath: filepath.Join(dataDir, "elements.conf"),
    ServicePath: filepath.Join("/etc/systemd/system", elementsServiceName+".service"),
    VersionPath: filepath.Join(root, "VERSION"),
    RPCCredsPath: filepath.Join(appDataDir, "rpc.env"),
    MainchainSourcePath: filepath.Join(appDataDir, "mainchain_source"),
  }
}

func (s *Server) installElements(ctx context.Context) error {
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
	if fileExists(paths.ServicePath) {
		_, _ = runSystemd(ctx, "systemctl", "disable", "--now", elementsServiceName)
		_, _ = runSystemd(ctx, "systemctl", "daemon-reload")
		_, _ = runSystemd(ctx, "/bin/sh", "-c", "rm -f "+paths.ServicePath)
	}
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
if [ -d /data ]; then
  if command -v setfacl >/dev/null 2>&1; then
    setfacl -m u:%[2]s:rx /data
  else
    chmod o+x /data
  fi
fi
mkdir -p "%[1]s"
chown %[2]s:%[2]s "%[1]s"
chmod 750 "%[1]s"
`, paths.DataDir, elementsUser)
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
  rpcUser, rpcPass, err := ensureElementsCredentials(paths)
  if err != nil {
    return err
  }
  mainchain, err := resolveElementsMainchainConfig(ctx, cfg, paths)
  if err != nil {
    return err
  }
  values := elementsConfigValues{
    RPCUser: rpcUser,
    RPCPass: rpcPass,
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
    "chain": "liquidv1",
    "daemon": "0",
    "server": "1",
    "listen": "1",
    "txindex": "1",
    "validatepegin": "1",
    "rpcuser": values.RPCUser,
    "rpcpassword": values.RPCPass,
    "rpcport": strconv.Itoa(elementsRPCPort),
    "mainchainrpchost": values.MainchainHost,
    "mainchainrpcport": strconv.Itoa(values.MainchainPort),
    "mainchainrpcuser": values.MainchainUser,
    "mainchainrpcpassword": values.MainchainPass,
    "fallbackfee": elementsFallbackFee,
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
  trimmed := strings.TrimSpace(host)
  if trimmed == "" {
    return "127.0.0.1", 8332
  }
  trimmed = strings.TrimPrefix(trimmed, "http://")
  trimmed = strings.TrimPrefix(trimmed, "https://")
  trimmed = strings.TrimPrefix(trimmed, "tcp://")
  if !strings.Contains(trimmed, ":") {
    return trimmed, 8332
  }
  parts := strings.Split(trimmed, ":")
  if len(parts) == 2 {
    port, err := strconv.Atoi(parts[1])
    if err != nil || port <= 0 {
      return parts[0], 8332
    }
    return parts[0], port
  }
  hostPart, portPart, err := net.SplitHostPort(trimmed)
  if err == nil {
    port, err := strconv.Atoi(portPart)
    if err != nil || port <= 0 {
      return hostPart, 8332
    }
    return hostPart, port
  }
  return trimmed, 8332
}

type elementsMainchainConfig struct {
  Source string
  Host string
  Port int
  User string
  Pass string
}

func resolveElementsMainchainConfig(ctx context.Context, cfg *config.Config, paths elementsPaths) (elementsMainchainConfig, error) {
  source := readElementsMainchainSource(paths)
  if source == "local" {
    localCfg, err := readLocalBitcoinRPCConfigFromFile()
    if err != nil {
      return elementsMainchainConfig{}, err
    }
    host, port := parseMainchainRPC(localCfg.Host)
    return elementsMainchainConfig{
      Source: "local",
      Host: host,
      Port: port,
      User: localCfg.User,
      Pass: localCfg.Pass,
    }, nil
  }
  host, port := parseMainchainRPC(cfg.BitcoinRemote.RPCHost)
  mainUser, mainPass := readBitcoinSecrets()
  if mainUser == "" || mainPass == "" {
    return elementsMainchainConfig{}, errors.New("bitcoin remote RPC credentials missing")
  }
  return elementsMainchainConfig{
    Source: "remote",
    Host: host,
    Port: port,
    User: mainUser,
    Pass: mainPass,
  }, nil
}

func readElementsMainchainSource(paths elementsPaths) string {
  raw, err := os.ReadFile(paths.MainchainSourcePath)
  if err != nil {
    return "remote"
  }
  source := strings.ToLower(strings.TrimSpace(string(raw)))
  if source != "local" && source != "remote" {
    return "remote"
  }
  return source
}

func writeElementsMainchainSource(paths elementsPaths, source string) error {
  normalized := strings.ToLower(strings.TrimSpace(source))
  if normalized != "local" && normalized != "remote" {
    return errors.New("invalid mainchain source")
  }
  return writeFile(paths.MainchainSourcePath, normalized+"\n", 0640)
}

func readLocalBitcoinRPCConfigFromFile() (bitcoinRPCConfig, error) {
  paths := bitcoinCoreAppPaths()
  raw, err := os.ReadFile(paths.ConfigPath)
  if err != nil {
    return bitcoinRPCConfig{}, fmt.Errorf("failed to read local bitcoin.conf: %w", err)
  }
  user, pass, _, _ := parseBitcoinCoreRPCConfig(string(raw))
  if user == "" || pass == "" {
    return bitcoinRPCConfig{}, errors.New("local RPC credentials missing")
  }
  port := parseBitcoinRPCPort(string(raw))
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
