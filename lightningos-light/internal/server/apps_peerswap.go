package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"lightningos-light/internal/system"
)

const (
	peerswapAppID         = "peerswap"
	peerswapAssetsVersion = "version_5_0"
	peerswapBundleVersion = "version_5_0_psweb_5_0_4"
	peerswapPSWebSHA256   = "439c5794d9c362bc0ea70bb864122fc30ae46357b37bf776c794902ef717cdd8"
	peerswapUser          = "losop"
	peerswapServiceName   = "lightningos-peerswapd"
	pswebServiceName      = "lightningos-psweb"
	pswebPort             = 1984
	peerswapAssetsArch    = "amd64"
)

type peerswapPaths struct {
	Root               string
	BinDir             string
	AppDataDir         string
	ConfigDir          string
	ConfigPath         string
	PSWebConfigPath    string
	ElementsSourcePath string
	ServicePath        string
	WebServicePath     string
	VersionPath        string
}

type peerswapApp struct {
	server *Server
}

type peerswapConfigValues struct {
	LndTLSPath          string
	LndMacaroonPath     string
	ElementsRPCUser     string
	ElementsRPCPass     string
	ElementsRPCHost     string
	ElementsRPCPort     int
	ElementsRPCWallet   string
	ElementsDataDir     string
	ElementsLiquidSwaps string
	BitcoinSwaps        string
}

func newPeerswapApp(s *Server) appHandler {
	return peerswapApp{server: s}
}

func peerswapDefinition() appDefinition {
	return appDefinition{
		ID:          peerswapAppID,
		Name:        "Peerswap",
		Description: "Peerswap daemon with psweb UI (local or remote Elements RPC).",
		Port:        pswebPort,
	}
}

func (a peerswapApp) Definition() appDefinition {
	return peerswapDefinition()
}

func (a peerswapApp) Info(ctx context.Context) (appInfo, error) {
	def := a.Definition()
	info := newAppInfo(def)
	paths := peerswapAppPaths()
	if !fileExists(paths.VersionPath) || !fileExists(filepath.Join(paths.BinDir, "peerswapd")) {
		return info, nil
	}
	info.Installed = true
	status, err := peerswapServiceStatus(ctx)
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func (a peerswapApp) Install(ctx context.Context) error {
	return a.server.installPeerswap(ctx)
}

func (a peerswapApp) Uninstall(ctx context.Context) error {
	return a.server.uninstallPeerswap(ctx)
}

func (a peerswapApp) Start(ctx context.Context) error {
	return a.server.startPeerswap(ctx)
}

func (a peerswapApp) Stop(ctx context.Context) error {
	return a.server.stopPeerswap(ctx)
}

func peerswapAppPaths() peerswapPaths {
	root := filepath.Join(appsRoot, peerswapAppID)
	return peerswapPaths{
		Root:               root,
		BinDir:             filepath.Join(root, "bin"),
		AppDataDir:         filepath.Join(appsDataRoot, peerswapAppID),
		ConfigDir:          filepath.Join("/home", peerswapUser, ".peerswap"),
		ConfigPath:         filepath.Join("/home", peerswapUser, ".peerswap", "peerswap.conf"),
		PSWebConfigPath:    filepath.Join("/home", peerswapUser, ".peerswap", "pswebconfig.json"),
		ElementsSourcePath: filepath.Join(appsDataRoot, peerswapAppID, "elements_rpc.json"),
		ServicePath:        filepath.Join("/etc/systemd/system", peerswapServiceName+".service"),
		WebServicePath:     filepath.Join("/etc/systemd/system", pswebServiceName+".service"),
		VersionPath:        filepath.Join(root, "VERSION"),
	}
}

func (s *Server) installPeerswap(ctx context.Context) error {
	return s.installPeerswapWithOptions(ctx, peerswapInstallOptions{})
}

func (s *Server) installPeerswapWithOptions(ctx context.Context, opts peerswapInstallOptions) error {
	paths := peerswapAppPaths()
	if err := os.MkdirAll(paths.Root, 0750); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}
	if err := os.MkdirAll(paths.AppDataDir, 0750); err != nil {
		return fmt.Errorf("failed to create app data directory: %w", err)
	}
	source, err := s.preparePeerswapElementsSourceForInstall(ctx, paths, opts)
	if err != nil {
		return err
	}
	if err := ensurePeerswapElementsDataDir(ctx); err != nil {
		return err
	}
	if err := ensurePeerswapConfigDir(ctx, paths); err != nil {
		return err
	}
	if err := ensurePeerswapBinaries(ctx, paths); err != nil {
		return err
	}
	if err := s.ensurePeerswapConfig(ctx, paths); err != nil {
		return err
	}
	if err := ensurePeerswapServices(ctx, paths, source.Mode); err != nil {
		return err
	}
	if _, err := runSystemd(ctx, "systemctl", "enable", "--now", peerswapServiceName); err != nil {
		return err
	}
	if _, err := runSystemd(ctx, "systemctl", "enable", "--now", pswebServiceName); err != nil {
		return err
	}
	if err := ensurePswebUfwAccess(ctx); err != nil && s.logger != nil {
		s.logger.Printf("psweb: ufw rule failed: %v", err)
	}
	return nil
}

func (s *Server) startPeerswap(ctx context.Context) error {
	paths := peerswapAppPaths()
	if !fileExists(paths.VersionPath) || !fileExists(filepath.Join(paths.BinDir, "peerswapd")) {
		return errors.New("Peerswap is not installed")
	}
	if err := os.MkdirAll(paths.AppDataDir, 0750); err != nil {
		return fmt.Errorf("failed to create app data directory: %w", err)
	}
	source, err := s.preparePeerswapElementsSourceForStart(ctx, paths)
	if err != nil {
		return err
	}
	if err := ensurePeerswapElementsDataDir(ctx); err != nil {
		return err
	}
	if err := ensurePeerswapConfigDir(ctx, paths); err != nil {
		return err
	}
	if err := ensurePeerswapBinaries(ctx, paths); err != nil {
		return err
	}
	if err := s.ensurePeerswapConfig(ctx, paths); err != nil {
		return err
	}
	if err := ensurePeerswapServices(ctx, paths, source.Mode); err != nil {
		return err
	}
	if _, err := runSystemd(ctx, "systemctl", "restart", peerswapServiceName); err != nil {
		return err
	}
	if _, err := runSystemd(ctx, "systemctl", "restart", pswebServiceName); err != nil {
		return err
	}
	if err := ensurePswebUfwAccess(ctx); err != nil && s.logger != nil {
		s.logger.Printf("psweb: ufw rule failed: %v", err)
	}
	return nil
}

func (s *Server) stopPeerswap(ctx context.Context) error {
	paths := peerswapAppPaths()
	if !fileExists(paths.VersionPath) || !fileExists(filepath.Join(paths.BinDir, "peerswapd")) {
		return errors.New("Peerswap is not installed")
	}
	_, _ = runSystemd(ctx, "systemctl", "stop", pswebServiceName)
	if _, err := runSystemd(ctx, "systemctl", "stop", peerswapServiceName); err != nil {
		return err
	}
	return nil
}

func ensurePswebUfwAccess(ctx context.Context) error {
	statusOut, err := system.RunCommandWithSudo(ctx, "ufw", "status")
	if err != nil || !strings.Contains(strings.ToLower(statusOut), "status: active") {
		return nil
	}
	_, err = system.RunCommandWithSudo(ctx, "ufw", "allow", fmt.Sprintf("%d/tcp", pswebPort))
	return err
}

func (s *Server) uninstallPeerswap(ctx context.Context) error {
	paths := peerswapAppPaths()
	if fileExists(paths.ServicePath) {
		_, _ = runSystemd(ctx, "systemctl", "disable", "--now", peerswapServiceName)
		_, _ = runSystemd(ctx, "systemctl", "disable", "--now", pswebServiceName)
		_, _ = runSystemd(ctx, "systemctl", "daemon-reload")
		_, _ = runSystemd(ctx, "/bin/sh", "-c", "rm -f "+paths.ServicePath+" "+paths.WebServicePath)
	}
	if _, err := runSystemd(ctx, "/bin/sh", "-c", "rm -rf "+paths.Root); err != nil {
		return fmt.Errorf("failed to remove app files: %w", err)
	}
	if _, err := runSystemd(ctx, "/bin/sh", "-c", "rm -rf "+paths.AppDataDir); err != nil {
		return fmt.Errorf("failed to remove app data: %w", err)
	}
	return nil
}

func ensureElementsReady(ctx context.Context) error {
	paths := elementsAppPaths()
	if !fileExists(paths.ElementsdPath) {
		return errors.New("Elements is required before installing Peerswap")
	}
	status, err := elementsServiceStatus(ctx)
	if err != nil {
		return fmt.Errorf("failed to check Elements status: %w", err)
	}
	if status != "running" {
		return errors.New("Elements must be running before starting Peerswap")
	}
	return nil
}

func ensurePeerswapConfigDir(ctx context.Context, paths peerswapPaths) error {
	script := fmt.Sprintf(`set -e
mkdir -p "%s"
chown %s:%s "%s"
chmod 750 "%s"
`, paths.ConfigDir, peerswapUser, peerswapUser, paths.ConfigDir, paths.ConfigDir)
	if _, err := runSystemd(ctx, "/bin/sh", "-c", script); err != nil {
		return fmt.Errorf("failed to prepare %s: %w", paths.ConfigDir, err)
	}
	return nil
}

func ensurePeerswapBinaries(ctx context.Context, paths peerswapPaths) error {
	if peerswapInstalledBinariesCurrent(paths) {
		return nil
	}
	assetsRoot, err := peerswapAssetsRoot()
	if err != nil {
		return err
	}
	if err := ensurePeerswapAssetStaging(ctx, assetsRoot); err != nil {
		return err
	}
	if err := ensurePeerswapPSWebChecksum(filepath.Join(assetsRoot, "psweb")); err != nil {
		return err
	}
	script := fmt.Sprintf(`set -e
mkdir -p "%s"
install -m 0755 "%s" "%s"
install -m 0755 "%s" "%s"
install -m 0755 "%s" "%s"
chown %s:%s "%s" "%s" "%s"
`, paths.BinDir,
		filepath.Join(assetsRoot, "peerswapd"), filepath.Join(paths.BinDir, "peerswapd"),
		filepath.Join(assetsRoot, "pscli"), filepath.Join(paths.BinDir, "pscli"),
		filepath.Join(assetsRoot, "psweb"), filepath.Join(paths.BinDir, "psweb"),
		peerswapUser, peerswapUser,
		filepath.Join(paths.BinDir, "peerswapd"), filepath.Join(paths.BinDir, "pscli"), filepath.Join(paths.BinDir, "psweb"))
	if _, err := runSystemd(ctx, "/bin/sh", "-c", script); err != nil {
		return err
	}
	return writeFile(paths.VersionPath, peerswapBundleVersion+"\n", 0640)
}

func peerswapInstalledBinariesCurrent(paths peerswapPaths) bool {
	if readSecretFile(paths.VersionPath) != peerswapBundleVersion {
		return false
	}
	if !peerswapBinariesExist(paths.BinDir) {
		return false
	}
	ok, err := peerswapFileSHA256Matches(filepath.Join(paths.BinDir, "psweb"), peerswapPSWebSHA256)
	return err == nil && ok
}

func peerswapAssetsRoot() (string, error) {
	base := filepath.Join("/opt/lightningos/manager/assets/binaries/peerswap", peerswapAssetsVersion, peerswapAssetsArch)
	return base, nil
}

func ensurePeerswapAssetStaging(ctx context.Context, dest string) error {
	if peerswapBinariesExist(dest) {
		ok, err := peerswapFileSHA256Matches(filepath.Join(dest, "psweb"), peerswapPSWebSHA256)
		if err == nil && ok {
			return nil
		}
	}
	script := fmt.Sprintf(`set -e
source=""
for candidate in \
  /home/*/brln-os-light/lightningos-light/assets/binaries/peerswap/%[2]s/%[3]s \
  /home/*/lightningos-light/assets/binaries/peerswap/%[2]s/%[3]s \
  /root/brln-os-light/lightningos-light/assets/binaries/peerswap/%[2]s/%[3]s \
  /root/lightningos-light/assets/binaries/peerswap/%[2]s/%[3]s; do
  if [ -f "$candidate/peerswapd" ] && [ -f "$candidate/pscli" ] && [ -f "$candidate/psweb" ]; then
    source="$candidate"
    break
  fi
done
if [ -n "$source" ]; then
  mkdir -p "%[1]s"
  install -m 0755 "$source/peerswapd" "%[1]s/peerswapd"
  install -m 0755 "$source/pscli" "%[1]s/pscli"
  install -m 0755 "$source/psweb" "%[1]s/psweb"
  exit 0
fi
if [ -f "%[1]s/peerswapd" ] && [ -f "%[1]s/pscli" ] && [ -f "%[1]s/psweb" ]; then
  exit 0
fi
echo "peerswap binaries not found under /home/*, /root, or %[1]s"
exit 1
`, dest, peerswapAssetsVersion, peerswapAssetsArch)
	if _, err := runSystemd(ctx, "/bin/sh", "-c", script); err != nil {
		return err
	}
	if peerswapBinariesExist(dest) {
		return nil
	}
	return fmt.Errorf("peerswap binaries missing in %s", dest)
}

func peerswapBinariesExist(dir string) bool {
	return fileExists(filepath.Join(dir, "peerswapd")) &&
		fileExists(filepath.Join(dir, "pscli")) &&
		fileExists(filepath.Join(dir, "psweb"))
}

func ensurePeerswapPSWebChecksum(path string) error {
	ok, err := peerswapFileSHA256Matches(path, peerswapPSWebSHA256)
	if err != nil {
		return fmt.Errorf("failed to verify staged psweb binary: %w", err)
	}
	if !ok {
		return fmt.Errorf("staged psweb binary at %s does not match expected psweb 5.0.4; refresh Peerswap assets and start again", path)
	}
	return nil
}

func peerswapFileSHA256Matches(path string, expected string) (bool, error) {
	actual, err := fileSHA256(path)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(actual, expected), nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func (s *Server) ensurePeerswapConfig(ctx context.Context, paths peerswapPaths) error {
	values, err := s.peerswapConfigDefaults(ctx)
	if err != nil {
		return err
	}
	raw, err := readPeerswapConfig(ctx, paths)
	if err != nil {
		return err
	}
	if raw != "" {
		values = applyPeerswapConfigOverrides(values, raw)
	}
	updated := raw
	if raw == "" {
		updated = defaultPeerswapConfig(values)
	} else {
		updated = updatePeerswapConfig(raw, values)
	}
	if updated != raw {
		if err := writePeerswapConfig(ctx, paths, updated); err != nil {
			return err
		}
	}
	return ensurePSWebConfig(ctx, paths, values)
}

func (s *Server) peerswapConfigDefaults(ctx context.Context) (peerswapConfigValues, error) {
	source, err := s.resolvePeerswapElementsSourceForConfig(ctx, peerswapAppPaths())
	if err != nil {
		return peerswapConfigValues{}, err
	}
	elementsPaths := elementsAppPaths()
	values := peerswapConfigValues{
		LndTLSPath:          "/data/lnd/tls.cert",
		LndMacaroonPath:     "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon",
		ElementsRPCWallet:   peerswapSourceWallet(source),
		ElementsDataDir:     elementsPaths.DataDir,
		ElementsLiquidSwaps: "true",
		BitcoinSwaps:        "false",
	}
	if source.Mode == peerswapElementsModeRemote {
		endpoint, err := normalizePeerswapRemoteEndpoint(source.URL)
		if err != nil {
			return peerswapConfigValues{}, err
		}
		values.ElementsRPCUser = source.User
		values.ElementsRPCPass = source.Password
		values.ElementsRPCHost = endpoint.Host
		values.ElementsRPCPort = endpoint.Port
		return values, nil
	}
	user, pass, port, err := readElementsRPCConfig(ctx)
	if err != nil {
		return peerswapConfigValues{}, err
	}
	values.ElementsRPCUser = user
	values.ElementsRPCPass = pass
	values.ElementsRPCHost = "http://127.0.0.1"
	values.ElementsRPCPort = port
	return values, nil
}

func ensurePeerswapElementsDataDir(ctx context.Context) error {
	return ensureElementsDataDir(ctx, elementsAppPaths())
}

func readElementsRPCConfig(ctx context.Context) (string, string, int, error) {
	paths := elementsAppPaths()
	raw, err := readElementsConfig(ctx, paths)
	if err != nil {
		return "", "", 0, err
	}
	if raw == "" {
		return "", "", 0, errors.New("elements.conf missing")
	}
	var user string
	var pass string
	port := elementsRPCPort
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
		case "rpcuser":
			user = value
		case "rpcpassword":
			pass = value
		case "rpcport":
			parsed, err := strconv.Atoi(value)
			if err == nil && parsed > 0 && parsed < 65536 {
				port = parsed
			}
		}
	}
	if user == "" || pass == "" {
		return "", "", 0, errors.New("elements RPC credentials missing")
	}
	return user, pass, port, nil
}

func defaultPeerswapConfig(values peerswapConfigValues) string {
	lines := []string{
		"# LightningOS Peerswap configuration",
		"lnd.tlscertpath=" + values.LndTLSPath,
		"lnd.macaroonpath=" + values.LndMacaroonPath,
		"elementsd.rpcuser=" + values.ElementsRPCUser,
		"elementsd.rpcpass=" + values.ElementsRPCPass,
		"elementsd.rpchost=" + values.ElementsRPCHost,
		"elementsd.rpcport=" + strconv.Itoa(values.ElementsRPCPort),
		"elementsd.rpcwallet=" + values.ElementsRPCWallet,
		"elementsd.datadir=" + values.ElementsDataDir,
		"elementsd.liquidswaps=" + values.ElementsLiquidSwaps,
		"bitcoinswaps=" + values.BitcoinSwaps,
		"",
	}
	return strings.Join(lines, "\n")
}

func applyPeerswapConfigOverrides(values peerswapConfigValues, raw string) peerswapConfigValues {
	if bitcoinSwaps, ok := readPeerswapConfigBoolString(raw, "bitcoinswaps"); ok {
		values.BitcoinSwaps = bitcoinSwaps
	}
	return values
}

func readPeerswapConfigBoolString(raw string, targetKey string) (string, bool) {
	value, ok := readPeerswapConfigValue(raw, targetKey)
	if !ok {
		return "", false
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return "", false
	}
	if parsed {
		return "true", true
	}
	return "false", true
}

func readPeerswapConfigValue(raw string, targetKey string) (string, bool) {
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
		if strings.EqualFold(key, targetKey) {
			return strings.TrimSpace(parts[1]), true
		}
	}
	return "", false
}

func updatePeerswapConfig(raw string, values peerswapConfigValues) string {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(strings.TrimRight(normalized, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = []string{}
	}
	forceOrder := []string{
		"lnd.tlscertpath",
		"lnd.macaroonpath",
		"elementsd.rpcuser",
		"elementsd.rpcpass",
		"elementsd.rpchost",
		"elementsd.rpcport",
		"elementsd.rpcwallet",
		"elementsd.datadir",
		"elementsd.liquidswaps",
		"bitcoinswaps",
	}
	force := map[string]string{
		"lnd.tlscertpath":       values.LndTLSPath,
		"lnd.macaroonpath":      values.LndMacaroonPath,
		"elementsd.rpcuser":     values.ElementsRPCUser,
		"elementsd.rpcpass":     values.ElementsRPCPass,
		"elementsd.rpchost":     values.ElementsRPCHost,
		"elementsd.rpcport":     strconv.Itoa(values.ElementsRPCPort),
		"elementsd.rpcwallet":   values.ElementsRPCWallet,
		"elementsd.datadir":     values.ElementsDataDir,
		"elementsd.liquidswaps": values.ElementsLiquidSwaps,
		"bitcoinswaps":          values.BitcoinSwaps,
	}
	seen := map[string]bool{}
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
		if value, ok := force[key]; ok {
			updated = append(updated, key+"="+value)
			seen[key] = true
			continue
		}
		updated = append(updated, line)
	}

	for _, key := range forceOrder {
		if !seen[key] {
			updated = append(updated, key+"="+force[key])
		}
	}

	return strings.Join(updated, "\n") + "\n"
}

func readPeerswapConfig(ctx context.Context, paths peerswapPaths) (string, error) {
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

func writePeerswapConfig(ctx context.Context, paths peerswapPaths, content string) error {
	tmpPath := filepath.Join(paths.Root, "peerswap.conf.tmp")
	if err := writeFile(tmpPath, content, 0640); err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	script := fmt.Sprintf("install -m 0600 -o %s -g %s %s %s", peerswapUser, peerswapUser, tmpPath, paths.ConfigPath)
	if _, err := runSystemd(ctx, "/bin/sh", "-c", script); err != nil {
		return err
	}
	return nil
}

func ensurePSWebConfig(ctx context.Context, paths peerswapPaths, values peerswapConfigValues) error {
	cfg, err := readPSWebConfig(ctx, paths)
	if err != nil {
		return err
	}

	bitcoinCfg, _, bitcoinErr := readBitcoinLocalRPCConfig(ctx)
	changed := updatePSWebConfigMap(cfg, paths, values, bitcoinCfg, bitcoinErr == nil)
	if !changed {
		return nil
	}
	return writePSWebConfig(ctx, paths, cfg)
}

func readPSWebConfig(ctx context.Context, paths peerswapPaths) (map[string]any, error) {
	out, err := runSystemd(ctx, "/bin/sh", "-c", "cat "+paths.PSWebConfigPath)
	if err != nil {
		msg := strings.ToLower(out + err.Error())
		if strings.Contains(msg, "no such file") || strings.Contains(msg, "not found") {
			return map[string]any{}, nil
		}
		return nil, err
	}
	raw := strings.TrimSpace(out)
	if raw == "" {
		return map[string]any{}, nil
	}
	cfg := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return map[string]any{}, nil
	}
	return cfg, nil
}

func updatePSWebConfigMap(cfg map[string]any, paths peerswapPaths, values peerswapConfigValues, bitcoinCfg bitcoinRPCConfig, hasBitcoin bool) bool {
	changed := false
	set := func(key string, value any) {
		if pswebConfigValueEqual(cfg[key], value) {
			return
		}
		cfg[key] = value
		changed = true
	}

	set("DataDir", paths.ConfigDir)
	set("ElementsUser", values.ElementsRPCUser)
	set("ElementsPass", values.ElementsRPCPass)
	set("BitcoinSwaps", strings.EqualFold(values.BitcoinSwaps, "true"))
	set("Chain", "mainnet")
	set("ElementsDir", values.ElementsDataDir)
	set("ElementsDirMapped", values.ElementsDataDir)
	set("ElementsHost", values.ElementsRPCHost)
	set("ElementsPort", strconv.Itoa(values.ElementsRPCPort))
	set("ElementsWallet", values.ElementsRPCWallet)
	set("LightningDir", "/data/lnd")

	if hasBitcoin {
		set("BitcoinHost", pswebBitcoinHost(bitcoinCfg.Host))
		set("BitcoinUser", bitcoinCfg.User)
		set("BitcoinPass", bitcoinCfg.Pass)
	} else if existingHost, ok := cfg["BitcoinHost"].(string); ok && shouldNormalizePSWebBitcoinHost(existingHost) {
		set("BitcoinHost", pswebBitcoinHost(existingHost))
	}

	return changed
}

func shouldNormalizePSWebBitcoinHost(host string) bool {
	return strings.Contains(host, ":8332:") ||
		strings.Contains(host, ":18332:") ||
		strings.Contains(host, ":18443:")
}

func pswebConfigValueEqual(existing any, desired any) bool {
	switch desiredValue := desired.(type) {
	case string:
		existingValue, ok := existing.(string)
		return ok && existingValue == desiredValue
	case bool:
		existingValue, ok := existing.(bool)
		return ok && existingValue == desiredValue
	default:
		return existing == desired
	}
}

func pswebBitcoinHost(host string) string {
	return "http://" + bitcoinRPCHostPort(host, 8332)
}

func writePSWebConfig(ctx context.Context, paths peerswapPaths, cfg map[string]any) error {
	content, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := filepath.Join(paths.Root, "pswebconfig.json.tmp")
	if err := writeFile(tmpPath, string(content)+"\n", 0640); err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	script := fmt.Sprintf("install -m 0644 -o %s -g %s %s %s", peerswapUser, peerswapUser, tmpPath, paths.PSWebConfigPath)
	if _, err := runSystemd(ctx, "/bin/sh", "-c", script); err != nil {
		return err
	}
	return nil
}

func ensurePeerswapServices(ctx context.Context, paths peerswapPaths, elementsMode string) error {
	svc := peerswapServiceContents(paths, elementsMode)
	if existing, err := os.ReadFile(paths.ServicePath); err == nil && string(existing) == svc {
		// no-op
	} else {
		tmpPath := filepath.Join(paths.Root, "peerswap.service.tmp")
		if err := writeFile(tmpPath, svc, 0644); err != nil {
			return err
		}
		defer func() {
			_ = os.Remove(tmpPath)
		}()
		script := fmt.Sprintf("install -m 0644 %s %s", tmpPath, paths.ServicePath)
		if _, err := runSystemd(ctx, "/bin/sh", "-c", script); err != nil {
			return err
		}
	}

	web := pswebServiceContents(paths)
	if existing, err := os.ReadFile(paths.WebServicePath); err == nil && string(existing) == web {
		// no-op
	} else {
		tmpPath := filepath.Join(paths.Root, "psweb.service.tmp")
		if err := writeFile(tmpPath, web, 0644); err != nil {
			return err
		}
		defer func() {
			_ = os.Remove(tmpPath)
		}()
		script := fmt.Sprintf("install -m 0644 %s %s", tmpPath, paths.WebServicePath)
		if _, err := runSystemd(ctx, "/bin/sh", "-c", script); err != nil {
			return err
		}
	}

	if _, err := runSystemd(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	return nil
}

func peerswapServiceContents(paths peerswapPaths, elementsModes ...string) string {
	elementsMode := peerswapElementsModeLocal
	if len(elementsModes) > 0 && elementsModes[0] != "" {
		elementsMode = elementsModes[0]
	}
	elementsAfter := ""
	if elementsMode != peerswapElementsModeRemote {
		elementsAfter = " " + elementsServiceName + ".service"
	}
	return fmt.Sprintf(`[Unit]
Description=LightningOS Peerswap daemon
After=network-online.target%s
Wants=network-online.target

[Service]
Type=simple
User=%s
Group=%s
SupplementaryGroups=lnd
Environment=HOME=/home/%s
Environment=USER=%s
WorkingDirectory=/home/%s
ExecStart=%s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`, elementsAfter, peerswapUser, peerswapUser, peerswapUser, peerswapUser, peerswapUser, filepath.Join(paths.BinDir, "peerswapd"))
}

func pswebServiceContents(paths peerswapPaths) string {
	return fmt.Sprintf(`[Unit]
Description=LightningOS Peerswap web UI
After=network-online.target %s
Wants=network-online.target

[Service]
Type=simple
User=%s
Group=%s
SupplementaryGroups=lnd
Environment=HOME=/home/%s
Environment=USER=%s
WorkingDirectory=/home/%s
ExecStart=%s -datadir %s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`, peerswapServiceName+".service", peerswapUser, peerswapUser, peerswapUser, peerswapUser, peerswapUser, filepath.Join(paths.BinDir, "psweb"), paths.ConfigDir)
}

func peerswapServiceStatus(ctx context.Context) (string, error) {
	daemonStatus, err := serviceActiveState(ctx, peerswapServiceName)
	if err != nil {
		return "unknown", err
	}
	webStatus, err := serviceActiveState(ctx, pswebServiceName)
	if err != nil {
		return "unknown", err
	}
	if daemonStatus == "running" && webStatus == "running" {
		return "running", nil
	}
	if daemonStatus == "stopped" && webStatus == "stopped" {
		return "stopped", nil
	}
	return "stopped", nil
}

func serviceActiveState(ctx context.Context, name string) (string, error) {
	out, err := runSystemd(ctx, "systemctl", "is-active", name)
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
	if state == "active" || state == "activating" {
		return "running", nil
	}
	if state == "inactive" || state == "failed" || state == "deactivating" {
		return "stopped", nil
	}
	return "unknown", nil
}
