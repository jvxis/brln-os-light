package appmanifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
)

const (
	PeerSwapID                 = "peerswap"
	PeerSwapVersion            = "v6.0.0-8-g816a5bb" // Unreleased post-v6.0.0 snapshot implementing protocol v7.
	PeerSwapCommit             = "816a5bb12d6ff2a08b667ca387cb8d4b3f706709"
	PeerSwapWebVersion         = "v6.0.0.1"
	PeerSwapWebCommit          = "09983da398f253f8c14213e9f5c61b80cc879b67"
	PeerSwapAssetDirectory     = "version_5_0" // Legacy package path retained for upgrade compatibility.
	PeerSwapAssetArch          = "amd64"
	PeerSwapUser               = "lightningos-peerswap"
	PeerSwapManagerGroup       = "lightningos"
	PeerSwapService            = "lightningos-peerswapd"
	PeerSwapWebService         = "lightningos-psweb"
	PeerSwapWebPort            = 1984
	PeerSwapRPCPort            = 42069
	PeerSwapStateRoot          = "/var/lib/lightningos"
	PeerSwapAppsRoot           = PeerSwapStateRoot + "/apps"
	PeerSwapAppsDataRoot       = PeerSwapStateRoot + "/apps-data"
	PeerSwapStagedAssetsRoot   = "/opt/lightningos/manager/assets/binaries/peerswap/" + PeerSwapAssetDirectory + "/" + PeerSwapAssetArch
	PeerSwapLegacyDataDir      = "/home/losop/.peerswap"
	PeerSwapElementsModeLocal  = "local"
	PeerSwapElementsModeRemote = "remote"
)

type PeerSwapPaths struct {
	Root               string
	BinDir             string
	DataRoot           string
	RuntimeDir         string
	LNDDir             string
	ConfigPath         string
	PSWebConfigPath    string
	ElementsSourcePath string
	ServicePath        string
	WebServicePath     string
	VersionPath        string
	PeerswapdPath      string
	PSCLIPath          string
	PSWebPath          string
	LNDMacaroonPath    string
	LNDTLSCertPath     string
	LegacyDataDir      string
}

type PeerSwapBinary struct {
	Name   string
	SHA256 string
}

func DefaultPeerSwapPaths() PeerSwapPaths {
	return PeerSwapPathsForRoots(PeerSwapAppsRoot, PeerSwapAppsDataRoot, "/etc/systemd/system")
}

func PeerSwapPathsForRoots(appsRoot, appsDataRoot, systemdRoot string) PeerSwapPaths {
	root := path.Join(appsRoot, PeerSwapID)
	dataRoot := path.Join(appsDataRoot, PeerSwapID)
	runtimeDir := path.Join(dataRoot, "runtime")
	lndDir := path.Join(runtimeDir, "lnd")
	return PeerSwapPaths{
		Root:               root,
		BinDir:             path.Join(root, "bin"),
		DataRoot:           dataRoot,
		RuntimeDir:         runtimeDir,
		LNDDir:             lndDir,
		ConfigPath:         path.Join(runtimeDir, "peerswap.conf"),
		PSWebConfigPath:    path.Join(runtimeDir, "pswebconfig.json"),
		ElementsSourcePath: path.Join(dataRoot, "elements_rpc.json"),
		ServicePath:        path.Join(systemdRoot, PeerSwapService+".service"),
		WebServicePath:     path.Join(systemdRoot, PeerSwapWebService+".service"),
		VersionPath:        path.Join(root, "VERSION"),
		PeerswapdPath:      path.Join(root, "bin", "peerswapd"),
		PSCLIPath:          path.Join(root, "bin", "pscli"),
		PSWebPath:          path.Join(root, "bin", "psweb"),
		LNDMacaroonPath:    path.Join(lndDir, "peerswap.macaroon"),
		LNDTLSCertPath:     path.Join(lndDir, "tls.cert"),
		LegacyDataDir:      PeerSwapLegacyDataDir,
	}
}

func PeerSwapBinaries() []PeerSwapBinary {
	return []PeerSwapBinary{
		{Name: "peerswapd", SHA256: "b3e131fe046e26a28581e08da241643788898330f2740bde459a4117390a443d"},
		{Name: "pscli", SHA256: "c6d8e2fb654ca56bdf318a9b50d2b8cb332cd231a4d9cfd6d308c85a65deca80"},
		{Name: "psweb", SHA256: "7409bff05d16b33929b1ea14d5356658fb201f5c73838a375d58650efde7ee3a"},
	}
}

func PeerSwapVersionMarker() string {
	return PeerSwapVersion + "_" + PeerSwapCommit + "_psweb_" + PeerSwapWebVersion + "_" + PeerSwapWebCommit
}

func PeerSwapServiceUnit(paths PeerSwapPaths, elementsMode string) string {
	elementsDependency := ""
	if elementsMode == PeerSwapElementsModeLocal {
		elementsDependency = " " + ElementsService + ".service"
	}
	return fmt.Sprintf(`[Unit]
Description=LightningOS PeerSwap daemon
After=network-online.target lnd.service%s
Wants=network-online.target

[Service]
Type=simple
User=%s
Group=%s
Environment=HOME=%s
Environment=USER=%s
WorkingDirectory=%s
ExecStart=%s --datadir %s --configfile %s
Restart=on-failure
RestartSec=5
TimeoutStopSec=15s
UMask=0077
PrivateTmp=true
PrivateDevices=true
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
MemoryDenyWriteExecute=true
CapabilityBoundingSet=
AmbientCapabilities=
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
ReadWritePaths=%s

[Install]
WantedBy=multi-user.target
`, elementsDependency, PeerSwapUser, PeerSwapUser, paths.RuntimeDir,
		PeerSwapUser, paths.RuntimeDir, paths.PeerswapdPath, paths.RuntimeDir, paths.ConfigPath,
		paths.RuntimeDir)
}

func PeerSwapWebServiceUnit(paths PeerSwapPaths) string {
	return fmt.Sprintf(`[Unit]
Description=LightningOS PeerSwap web UI
After=network-online.target %s.service
Wants=network-online.target

[Service]
Type=simple
User=%s
Group=%s
Environment=HOME=%s
Environment=USER=%s
WorkingDirectory=%s
ExecStart=%s -datadir %s
Restart=on-failure
RestartSec=5
UMask=0077
PrivateTmp=true
PrivateDevices=true
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
MemoryDenyWriteExecute=true
CapabilityBoundingSet=
AmbientCapabilities=
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
ReadWritePaths=%s

[Install]
WantedBy=multi-user.target
`, PeerSwapService, PeerSwapUser, PeerSwapUser, paths.RuntimeDir,
		PeerSwapUser, paths.RuntimeDir, paths.PSWebPath, paths.RuntimeDir,
		paths.RuntimeDir)
}

func ValidatePeerSwapMaterial(tlsCertificate, macaroon []byte) error {
	if len(tlsCertificate) == 0 || len(tlsCertificate) > 16*1024 {
		return errors.New("invalid PeerSwap LND TLS certificate")
	}
	if len(macaroon) > 16*1024 {
		return errors.New("invalid PeerSwap LND macaroon")
	}
	return nil
}

func ValidatePeerSwapSource(mode, rawURL, user, password, wallet string) error {
	if mode != PeerSwapElementsModeLocal && mode != PeerSwapElementsModeRemote {
		return errors.New("invalid PeerSwap Elements source mode")
	}
	if len(wallet) > 128 || strings.ContainsAny(wallet, "\r\n\x00") {
		return errors.New("invalid PeerSwap Elements wallet")
	}
	if mode == PeerSwapElementsModeLocal {
		if rawURL != "" || user != "" || password != "" || wallet != "peerswap" {
			return errors.New("local PeerSwap Elements source cannot contain remote credentials")
		}
		return nil
	}
	if len(rawURL) > 2048 || len(user) == 0 || len(user) > 256 || len(password) == 0 || len(password) > 2048 || strings.ContainsAny(user+password, "\r\n\x00") {
		return errors.New("invalid remote PeerSwap Elements credentials")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("invalid remote PeerSwap Elements URL")
	}
	return nil
}

func ValidatePeerSwapWebConfig(content string, paths PeerSwapPaths) error {
	if len(content) == 0 || len(content) > 64*1024 {
		return errors.New("invalid PeerSwap web configuration")
	}
	values := map[string]any{}
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&values); err != nil {
		return errors.New("invalid PeerSwap web configuration")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("invalid PeerSwap web configuration")
	}
	allowed := map[string]struct{}{
		"DataDir": {}, "ElementsUser": {}, "ElementsPass": {}, "BitcoinSwaps": {},
		"Chain": {}, "ElementsDir": {}, "ElementsDirMapped": {}, "ElementsHost": {},
		"ElementsPort": {}, "ElementsWallet": {}, "LightningDir": {}, "BitcoinHost": {},
		"BitcoinUser": {}, "BitcoinPass": {},
	}
	for key := range values {
		if _, ok := allowed[key]; !ok {
			return errors.New("PeerSwap web configuration option is not allowed")
		}
	}
	required := map[string]string{
		"DataDir":      paths.RuntimeDir,
		"LightningDir": paths.LNDDir,
		"Chain":        "mainnet",
	}
	for key, expected := range required {
		value, ok := values[key].(string)
		if !ok || value != expected {
			return fmt.Errorf("PeerSwap web configuration %s is not allowed", key)
		}
	}
	elementsDir, ok := values["ElementsDir"].(string)
	if !ok {
		return errors.New("PeerSwap web Elements directory is required")
	}
	normalizedElementsDir, err := NormalizeElementsDataDir(elementsDir)
	if err != nil || normalizedElementsDir != elementsDir || values["ElementsDirMapped"] != elementsDir {
		return errors.New("PeerSwap web Elements directory is invalid")
	}
	for _, key := range []string{"ElementsUser", "ElementsPass", "ElementsWallet"} {
		value, ok := values[key].(string)
		if !ok || strings.TrimSpace(value) == "" || len(value) > 2048 || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("PeerSwap web configuration %s is invalid", key)
		}
	}
	if err := validatePeerSwapWebEndpoint(values["ElementsHost"], values["ElementsPort"]); err != nil {
		return err
	}
	bitcoinSwaps, ok := values["BitcoinSwaps"].(bool)
	if !ok {
		return errors.New("PeerSwap web BitcoinSwaps is invalid")
	}
	if bitcoinHost, present := values["BitcoinHost"]; present {
		if err := validatePeerSwapWebEndpoint(bitcoinHost, nil); err != nil {
			return err
		}
	}
	if bitcoinSwaps {
		for _, key := range []string{"BitcoinHost", "BitcoinUser", "BitcoinPass"} {
			value, ok := values[key].(string)
			if !ok || strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\r\n\x00") {
				return fmt.Errorf("PeerSwap web configuration %s is required", key)
			}
		}
	}
	return nil
}

func validatePeerSwapWebEndpoint(rawHost any, rawPort any) error {
	host, ok := rawHost.(string)
	if !ok {
		return errors.New("PeerSwap web RPC host is invalid")
	}
	parsed, err := url.Parse(host)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("PeerSwap web RPC host is invalid")
	}
	if rawPort == nil {
		return nil
	}
	port, ok := rawPort.(string)
	if !ok {
		return errors.New("PeerSwap web RPC port is invalid")
	}
	parsedPort, err := strconv.Atoi(port)
	if err != nil || parsedPort < 1 || parsedPort > 65535 {
		return errors.New("PeerSwap web RPC port is invalid")
	}
	return nil
}

func ValidatePeerSwapConfig(content, elementsMode string, paths PeerSwapPaths) error {
	if len(content) == 0 || len(content) > 64*1024 {
		return errors.New("invalid PeerSwap configuration")
	}
	if elementsMode != PeerSwapElementsModeLocal && elementsMode != PeerSwapElementsModeRemote {
		return errors.New("invalid PeerSwap Elements source mode")
	}
	values, err := peerSwapConfigValues(content)
	if err != nil {
		return err
	}
	required := map[string]string{
		"host":                  "127.0.0.1:" + strconv.Itoa(PeerSwapRPCPort),
		"lnd.host":              "127.0.0.1:10009",
		"lnd.tlscertpath":       paths.LNDTLSCertPath,
		"lnd.macaroonpath":      paths.LNDMacaroonPath,
		"elementsd.liquidswaps": "true",
	}
	for key, expected := range required {
		if values[key] != expected {
			return fmt.Errorf("PeerSwap configuration %s is not allowed", key)
		}
	}
	for _, key := range []string{"elementsd.rpcuser", "elementsd.rpcpass", "elementsd.rpchost", "elementsd.rpcport", "elementsd.rpcwallet"} {
		if strings.TrimSpace(values[key]) == "" {
			return fmt.Errorf("PeerSwap configuration %s is required", key)
		}
	}
	port, err := strconv.Atoi(values["elementsd.rpcport"])
	if err != nil || port < 1 || port > 65535 {
		return errors.New("PeerSwap Elements RPC port is invalid")
	}
	host, err := url.Parse(values["elementsd.rpchost"])
	if err != nil || (host.Scheme != "http" && host.Scheme != "https") || host.Host == "" || host.User != nil || host.RawQuery != "" || host.Fragment != "" || (host.Path != "" && host.Path != "/") {
		return errors.New("PeerSwap Elements RPC host is invalid")
	}
	if elementsMode == PeerSwapElementsModeLocal && values["elementsd.rpchost"] != "http://127.0.0.1" {
		return errors.New("local PeerSwap Elements RPC must use loopback")
	}
	if dataDir := strings.TrimSpace(values["elementsd.datadir"]); dataDir != "" && (!strings.HasPrefix(dataDir, "/") || path.Clean(dataDir) != dataDir) {
		return errors.New("PeerSwap Elements data directory is invalid")
	}
	if _, err := strconv.ParseBool(values["bitcoinswaps"]); err != nil {
		return errors.New("PeerSwap bitcoinswaps value is invalid")
	}
	return nil
}

func MergePeerSwapConfig(existing, desired, elementsMode string, paths PeerSwapPaths) (string, error) {
	if err := ValidatePeerSwapConfig(desired, elementsMode, paths); err != nil {
		return "", err
	}
	desiredValues, _ := peerSwapConfigValues(desired)
	if existingValues, err := peerSwapConfigValues(existing); err == nil {
		if value, ok := existingValues["bitcoinswaps"]; ok {
			if _, parseErr := strconv.ParseBool(value); parseErr == nil {
				desiredValues["bitcoinswaps"] = strings.ToLower(value)
			}
		}
	}
	managedOrder := []string{"host", "lnd.host", "lnd.tlscertpath", "lnd.macaroonpath", "elementsd.rpcuser", "elementsd.rpcpass", "elementsd.rpchost", "elementsd.rpcport", "elementsd.rpcwallet", "elementsd.datadir", "elementsd.liquidswaps", "bitcoinswaps"}
	managed := make(map[string]struct{}, len(managedOrder))
	for _, key := range managedOrder {
		managed[key] = struct{}{}
	}
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(existing, "\r\n", "\n"), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	out := make([]string, 0, len(lines)+len(managedOrder))
	seen := make(map[string]bool, len(managedOrder))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			out = append(out, line)
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		if _, ok := managed[key]; !ok {
			out = append(out, line)
			continue
		}
		if seen[key] {
			continue
		}
		out = append(out, key+"="+desiredValues[key])
		seen[key] = true
	}
	for _, key := range managedOrder {
		if !seen[key] {
			out = append(out, key+"="+desiredValues[key])
		}
	}
	merged := strings.Join(out, "\n") + "\n"
	if err := ValidatePeerSwapConfig(merged, elementsMode, paths); err != nil {
		return "", err
	}
	return merged, nil
}

func MergePeerSwapWebConfig(existing, desired string, bitcoinSwaps bool) (string, error) {
	if len(existing) > 64*1024 || len(desired) == 0 || len(desired) > 64*1024 {
		return "", errors.New("invalid PeerSwap web configuration")
	}
	current := map[string]any{}
	if strings.TrimSpace(existing) != "" {
		if err := json.Unmarshal([]byte(existing), &current); err != nil {
			return "", errors.New("invalid existing PeerSwap web configuration")
		}
	}
	patch := map[string]any{}
	if err := json.Unmarshal([]byte(desired), &patch); err != nil {
		return "", errors.New("invalid PeerSwap web configuration")
	}
	for key, value := range patch {
		current[key] = value
	}
	current["BitcoinSwaps"] = bitcoinSwaps
	raw, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return "", errors.New("invalid PeerSwap web configuration")
	}
	return string(raw) + "\n", nil
}

func peerSwapConfigValues(content string) (map[string]string, error) {
	values := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		if len(key) > 128 || len(value) > 2048 {
			return nil, errors.New("invalid PeerSwap configuration value")
		}
		values[key] = value
	}
	return values, nil
}
