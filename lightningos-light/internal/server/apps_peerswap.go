package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/lndclient"
	"lightningos-light/internal/system"
)

const (
	peerswapAppID         = "peerswap"
	peerswapAssetsVersion = "version_5_0"
	peerswapUser          = appmanifest.PeerSwapUser
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
		SecurityNotices: []string{
			appSecurityNoticeElevatedLNDAccess,
		},
	}
}

func (a peerswapApp) Definition() appDefinition {
	return peerswapDefinition()
}

func (a peerswapApp) Info(ctx context.Context) (appInfo, error) {
	def := a.Definition()
	info := newAppInfo(def)
	handled, state, err := system.PeerSwapStatusWithBroker(ctx)
	if !handled {
		return info, errors.New("PeerSwap requires the privileged broker in enforce mode")
	}
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Installed = state.Installed
	info.Status = state.Status
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
	manifest := appmanifest.DefaultPeerSwapPaths()
	return peerswapPaths{
		Root:               manifest.Root,
		BinDir:             manifest.BinDir,
		AppDataDir:         manifest.DataRoot,
		ConfigDir:          manifest.RuntimeDir,
		ConfigPath:         manifest.ConfigPath,
		PSWebConfigPath:    manifest.PSWebConfigPath,
		ElementsSourcePath: manifest.ElementsSourcePath,
		ServicePath:        manifest.ServicePath,
		WebServicePath:     manifest.WebServicePath,
		VersionPath:        manifest.VersionPath,
	}
}

func (s *Server) installPeerswap(ctx context.Context) error {
	return s.installPeerswapWithOptions(ctx, peerswapInstallOptions{})
}

func (s *Server) installPeerswapWithOptions(ctx context.Context, opts peerswapInstallOptions) error {
	paths := peerswapAppPaths()
	source, err := s.preparePeerswapElementsSourceForInstall(ctx, paths, opts)
	if err != nil {
		return err
	}
	if err := s.ensurePeerswapBrokerRuntime(ctx, paths, source); err != nil {
		return err
	}
	if handled, err := system.PeerSwapLifecycleWithBroker(ctx, "start"); !handled {
		return errors.New("PeerSwap requires the privileged broker in enforce mode")
	} else if err != nil {
		return err
	}
	if handled, _, firewallErr := system.EnsureAppFirewallWithBroker(ctx, peerswapAppID); handled && firewallErr != nil && s.logger != nil {
		s.logger.Printf("psweb: firewall rule failed: %v", firewallErr)
	}
	return nil
}

func (s *Server) startPeerswap(ctx context.Context) error {
	paths := peerswapAppPaths()
	handled, state, err := system.PeerSwapStatusWithBroker(ctx)
	if !handled {
		return errors.New("PeerSwap requires the privileged broker in enforce mode")
	}
	if err != nil {
		return err
	}
	if !state.Installed {
		return errors.New("Peerswap is not installed")
	}
	source, err := s.preparePeerswapElementsSourceForStart(ctx, paths)
	if err != nil {
		return err
	}
	if err := s.ensurePeerswapBrokerRuntime(ctx, paths, source); err != nil {
		return err
	}
	if handled, err := system.PeerSwapLifecycleWithBroker(ctx, "start"); !handled {
		return errors.New("PeerSwap requires the privileged broker in enforce mode")
	} else if err != nil {
		return err
	}
	if handled, _, firewallErr := system.EnsureAppFirewallWithBroker(ctx, peerswapAppID); handled && firewallErr != nil && s.logger != nil {
		s.logger.Printf("psweb: firewall rule failed: %v", firewallErr)
	}
	return nil
}

func (s *Server) stopPeerswap(ctx context.Context) error {
	handled, state, err := system.PeerSwapStatusWithBroker(ctx)
	if !handled {
		return errors.New("PeerSwap requires the privileged broker in enforce mode")
	}
	if err != nil {
		return err
	}
	if !state.Installed {
		return errors.New("Peerswap is not installed")
	}
	if handled, err := system.PeerSwapLifecycleWithBroker(ctx, "stop"); !handled {
		return errors.New("PeerSwap requires the privileged broker in enforce mode")
	} else {
		return err
	}
}

func (s *Server) uninstallPeerswap(ctx context.Context) error {
	if handled, err := system.RemovePeerSwapWithBroker(ctx); !handled {
		return errors.New("PeerSwap requires the privileged broker in enforce mode")
	} else {
		return err
	}
}

func peerswapBinariesExist(dir string) bool {
	return fileExists(filepath.Join(dir, "peerswapd")) &&
		fileExists(filepath.Join(dir, "pscli")) &&
		fileExists(filepath.Join(dir, "psweb"))
}

func ensurePeerswapBinaryChecksums(dir string) error {
	expected := peerswapBinarySHA256s()
	for name, sha := range expected {
		path := filepath.Join(dir, name)
		ok, err := peerswapFileSHA256Matches(path, sha)
		if err != nil {
			return fmt.Errorf("failed to verify staged %s binary: %w", name, err)
		}
		if !ok {
			return fmt.Errorf("staged %s binary at %s does not match expected bundle %s; refresh Peerswap assets and start again", name, path, appmanifest.PeerSwapVersionMarker())
		}
	}
	return nil
}

func peerswapBinaryChecksumsMatch(dir string) bool {
	for name, sha := range peerswapBinarySHA256s() {
		ok, err := peerswapFileSHA256Matches(filepath.Join(dir, name), sha)
		if err != nil || !ok {
			return false
		}
	}
	return true
}

func peerswapBinarySHA256s() map[string]string {
	checksums := make(map[string]string, len(appmanifest.PeerSwapBinaries()))
	for _, binary := range appmanifest.PeerSwapBinaries() {
		checksums[binary.Name] = binary.SHA256
	}
	return checksums
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

func (s *Server) ensurePeerswapBrokerRuntime(ctx context.Context, paths peerswapPaths, source peerswapElementsSource) error {
	values, err := s.peerswapConfigDefaults(ctx)
	if err != nil {
		return err
	}
	config := defaultPeerswapConfig(values)
	webConfig, err := buildPeerswapWebConfig(ctx, paths, values)
	if err != nil {
		return err
	}
	handled, state, err := system.PeerSwapStatusWithBroker(ctx)
	if !handled {
		return errors.New("PeerSwap requires the privileged broker in enforce mode")
	}
	if err != nil {
		return err
	}
	certificate, macaroon, err := s.peerSwapLNDMaterial(ctx, state.HasLNDMacaroon)
	if err != nil {
		return err
	}
	handled, err = system.EnsurePeerSwapWithBroker(ctx, source.Mode, config, webConfig, certificate, macaroon)
	if !handled {
		return errors.New("PeerSwap requires the privileged broker in enforce mode")
	}
	return err
}

func (s *Server) peerswapConfigDefaults(ctx context.Context) (peerswapConfigValues, error) {
	paths := peerswapAppPaths()
	source, err := s.resolvePeerswapElementsSourceForConfig(ctx, paths)
	if err != nil {
		return peerswapConfigValues{}, err
	}
	elementsPaths := elementsAppPaths()
	values := peerswapConfigValues{
		LndTLSPath:          appmanifest.DefaultPeerSwapPaths().LNDTLSCertPath,
		LndMacaroonPath:     appmanifest.DefaultPeerSwapPaths().LNDMacaroonPath,
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
		wallet, err := s.defaultPeerswapRemoteWallet(ctx)
		if err != nil {
			return peerswapConfigValues{}, err
		}
		values.ElementsRPCWallet = wallet
		if source.Wallet != wallet {
			source.Wallet = wallet
			if err := writePeerswapElementsSource(ctx, paths, source); err != nil {
				return peerswapConfigValues{}, err
			}
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

func (s *Server) peerSwapLNDMaterial(ctx context.Context, hasExistingMacaroon bool) ([]byte, []byte, error) {
	certificate, err := os.ReadFile("/data/lnd/tls.cert")
	if err != nil || len(certificate) == 0 {
		return nil, nil, errors.New("LND TLS certificate is unavailable")
	}
	if hasExistingMacaroon {
		return certificate, nil, nil
	}
	if s == nil || s.lnd == nil {
		return nil, nil, errors.New("LND client unavailable")
	}
	ids, err := s.lnd.ListMacaroonIDs(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list LND macaroon IDs: %w", err)
	}
	rootKeyID, err := lndclient.GenerateMacaroonRootKeyID(ids, time.Now())
	if err != nil {
		return nil, nil, err
	}
	baked, err := s.lnd.BakeCustomMacaroon(ctx, lndclient.BakeCustomMacaroonRequest{
		Permissions: peerSwapMacaroonPermissions(),
		RootKeyID:   rootKeyID,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to bake dedicated PeerSwap macaroon: %w", err)
	}
	macaroon, err := hex.DecodeString(baked.MacaroonHex)
	if err != nil || len(macaroon) == 0 {
		return nil, nil, errors.New("invalid LND macaroon response")
	}
	if err := validatePeerSwapCredentialNotAdmin(macaroon); err != nil {
		return nil, nil, err
	}
	return certificate, macaroon, nil
}

func validatePeerSwapCredentialNotAdmin(credential []byte) error {
	equal, err := lndCredentialEqualsNativeAdmin(credential)
	if err != nil {
		return err
	}
	if equal {
		return errors.New("PeerSwap LND credential must not be the admin macaroon")
	}
	return nil
}

func peerSwapMacaroonPermissions() []lndclient.MacaroonPermission {
	return []lndclient.MacaroonPermission{
		{Entity: "address", Action: "write"},
		{Entity: "info", Action: "read"},
		{Entity: "invoices", Action: "read"},
		{Entity: "invoices", Action: "write"},
		{Entity: "offchain", Action: "read"},
		{Entity: "offchain", Action: "write"},
		{Entity: "onchain", Action: "read"},
		{Entity: "onchain", Action: "write"},
		{Entity: "peers", Action: "read"},
	}
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
		"host=127.0.0.1:" + strconv.Itoa(appmanifest.PeerSwapRPCPort),
		"lnd.host=127.0.0.1:10009",
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
		"host",
		"lnd.host",
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
		"host":                  "127.0.0.1:" + strconv.Itoa(appmanifest.PeerSwapRPCPort),
		"lnd.host":              "127.0.0.1:10009",
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

func buildPeerswapWebConfig(ctx context.Context, paths peerswapPaths, values peerswapConfigValues) (string, error) {
	cfg := map[string]any{}
	bitcoinCfg, bitcoinErr := readBitcoinLocalRPCConfig(ctx)
	updatePSWebConfigMap(cfg, paths, values, bitcoinCfg, bitcoinErr == nil)
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw) + "\n", nil
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
	set("LightningDir", appmanifest.DefaultPeerSwapPaths().LNDDir)

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

func peerswapServiceContents(paths peerswapPaths, elementsModes ...string) string {
	elementsMode := peerswapElementsModeLocal
	if len(elementsModes) > 0 && elementsModes[0] != "" {
		elementsMode = elementsModes[0]
	}
	manifest := appmanifest.DefaultPeerSwapPaths()
	if paths.BinDir != "" {
		manifest.BinDir = paths.BinDir
		manifest.PeerswapdPath = filepath.Join(paths.BinDir, "peerswapd")
	}
	if paths.ConfigDir != "" {
		manifest.RuntimeDir = paths.ConfigDir
	}
	return appmanifest.PeerSwapServiceUnit(manifest, elementsMode)
}

func pswebServiceContents(paths peerswapPaths) string {
	manifest := appmanifest.DefaultPeerSwapPaths()
	if paths.BinDir != "" {
		manifest.BinDir = paths.BinDir
		manifest.PSWebPath = filepath.Join(paths.BinDir, "psweb")
	}
	if paths.ConfigDir != "" {
		manifest.RuntimeDir = paths.ConfigDir
	}
	return appmanifest.PeerSwapWebServiceUnit(manifest)
}
