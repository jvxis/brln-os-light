package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"lightningos-light/internal/config"
	"lightningos-light/internal/system"
)

const (
	elementsAppID            = "elements"
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
	handled, err := s.prepareElementsWithBroker(ctx, paths)
	if !handled {
		return errors.New("Elements preparation requires privileged broker enforce mode")
	}
	if err != nil {
		return err
	}
	if lifecycleHandled, lifecycleErr := system.ElementsLifecycleWithBroker(ctx, paths.DataDir, "start"); lifecycleHandled {
		return lifecycleErr
	}
	return errors.New("Elements lifecycle requires privileged broker enforce mode")
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

func (s *Server) startElements(ctx context.Context) error {
	paths := elementsAppPaths()
	if !fileExists(paths.ElementsdPath) {
		return errors.New("Elements is not installed")
	}
	handled, err := s.prepareElementsWithBroker(ctx, paths)
	if !handled {
		return errors.New("Elements preparation requires privileged broker enforce mode")
	}
	if err != nil {
		return err
	}
	if lifecycleHandled, lifecycleErr := system.ElementsLifecycleWithBroker(ctx, paths.DataDir, "start"); lifecycleHandled {
		return lifecycleErr
	}
	return errors.New("Elements lifecycle requires privileged broker enforce mode")
}

func (s *Server) stopElements(ctx context.Context) error {
	paths := elementsAppPaths()
	if !fileExists(paths.ElementsdPath) {
		return errors.New("Elements is not installed")
	}
	if handled, err := system.ElementsLifecycleWithBroker(ctx, paths.DataDir, "stop"); handled {
		return err
	}
	return errors.New("Elements lifecycle requires privileged broker enforce mode")
}

func (s *Server) uninstallElements(ctx context.Context) error {
	paths := elementsAppPaths()
	if handled, err := system.RemoveElementsWithBroker(ctx, paths.DataDir); handled {
		if err != nil {
			return err
		}
		if err := os.RemoveAll(paths.AppDataDir); err != nil {
			return fmt.Errorf("failed to remove app data: %w", err)
		}
		return nil
	}
	return errors.New("Elements removal requires privileged broker enforce mode")
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
	if handled, err := system.EnsureElementsWithBroker(ctx, paths.DataDir, defaultElementsConfig(values)); handled {
		return err
	}
	return errors.New("Elements config write requires privileged broker enforce mode")
}

func (s *Server) prepareElementsWithBroker(ctx context.Context, paths elementsPaths) (bool, error) {
	if s == nil || s.cfg == nil {
		return false, errors.New("config unavailable")
	}
	if err := os.MkdirAll(paths.AppDataDir, 0750); err != nil {
		return false, fmt.Errorf("failed to create app data directory: %w", err)
	}
	rpcUser, rpcPass, err := ensureElementsCredentials(paths)
	if err != nil {
		return false, err
	}
	mainchain, err := resolveElementsMainchainConfig(ctx, s.cfg, paths)
	if err != nil {
		return false, err
	}
	if storedSource, sourceSet := readElementsMainchainSourceState(paths); !sourceSet || storedSource != mainchain.Source {
		if err := writeElementsMainchainSource(paths, mainchain.Source); err != nil {
			return false, err
		}
	}
	values := elementsConfigValues{
		RPCUser: rpcUser, RPCPass: rpcPass, MainchainHost: mainchain.Host, MainchainPort: mainchain.Port,
		MainchainUser: mainchain.User, MainchainPass: mainchain.Pass,
	}
	return system.EnsureElementsWithBroker(ctx, paths.DataDir, defaultElementsConfig(values))
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

func readElementsConfig(ctx context.Context, paths elementsPaths) (string, error) {
	if handled, content, err := system.ReadElementsConfigWithBroker(ctx, paths.DataDir); handled {
		return content, err
	}
	return "", errors.New("Elements config read requires privileged broker enforce mode")
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
	if cfg, err := readBitcoinLocalRPCConfig(ctx); err == nil {
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
	raw, err := readBitcoinCoreConfigRaw(ctx, paths)
	if err != nil {
		return bitcoinRPCConfig{}, fmt.Errorf("failed to read local bitcoin.conf: %w", err)
	}
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
	paths := elementsAppPaths()
	if handled, raw, err := system.ElementsStatusWithBroker(ctx, paths.DataDir); handled {
		if err != nil {
			return "unknown", err
		}
		var state struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			return "unknown", errors.New("invalid Elements broker status")
		}
		return state.Status, nil
	}
	return "unknown", errors.New("Elements status requires privileged broker enforce mode")
}
