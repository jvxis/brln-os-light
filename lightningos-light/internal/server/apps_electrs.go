package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/system"
)

const (
	electrsAppID                       = appmanifest.ElectrsID
	electrsRPCPort                     = appmanifest.ElectrsRPCPort
	electrsMonitorPort                 = appmanifest.ElectrsMonitorPort
	electrsCredentialActivationTimeout = 90 * time.Second
)

var errElectrsBitcoinRestartConfirmationRequired = errors.New("confirm the possible one-time Bitcoin Core restart before continuing")

type electrsInstallOptions struct {
	ConfirmBitcoinRestart bool   `json:"confirm_bitcoin_restart"`
	StorageMount          string `json:"storage_mount"`
}

type electrsPaths struct {
	Root        string
	ComposePath string
	EnvPath     string
	CookiePath  string
}

type electrsRuntimeValues struct {
	BitcoinRPCUser string
	BitcoinRPCPass string
	BitcoinRPCHost string
	BitcoinRPCPort int
	Network        string
	BitcoinP2PHost string
	BitcoinP2PPort int
	BitcoinMode    string
}

type electrsApp struct {
	server *Server
}

func newElectrsApp(s *Server) appHandler {
	return electrsApp{server: s}
}

func electrsDefinition() appDefinition {
	return appDefinition{
		ID:          electrsAppID,
		Name:        "Electrs",
		Description: "Electrum server (romanz/electrs) indexing the local Bitcoin Core node.",
		Port:        0,
	}
}

func (a electrsApp) Definition() appDefinition {
	return electrsDefinition()
}

func (a electrsApp) Info(ctx context.Context) (appInfo, error) {
	def := a.Definition()
	info := newAppInfo(def)
	paths := electrsAppPaths()
	if !fileExists(paths.ComposePath) {
		return info, nil
	}
	info.Installed = true
	handled, status, _, err := system.InspectAppWithBroker(ctx, electrsAppID)
	if !handled {
		info.Status = "unknown"
		return info, errors.New("Electrs status requires privileged broker enforce mode")
	}
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func (a electrsApp) Install(ctx context.Context) error {
	return a.server.applyElectrs(ctx, false, "")
}

func (a electrsApp) Start(ctx context.Context) error {
	return a.server.applyElectrs(ctx, false, "")
}

func (a electrsApp) Stop(ctx context.Context) error {
	paths := electrsAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("Electrs is not installed")
	}
	if handled, err := system.AppLifecycleWithBroker(ctx, electrsAppID, "stop"); !handled {
		return errors.New("Electrs lifecycle requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("Electrs stop failed: %w", err)
	}
	return nil
}

func (a electrsApp) Uninstall(ctx context.Context) error {
	paths := electrsAppPaths()
	if fileExists(paths.ComposePath) {
		if handled, err := system.RemoveAppWithBroker(ctx, electrsAppID); !handled {
			return errors.New("Electrs removal requires privileged broker enforce mode")
		} else if err != nil {
			return fmt.Errorf("Electrs removal failed: %w", err)
		}
	}
	if err := os.RemoveAll(paths.Root); err != nil {
		return fmt.Errorf("failed to remove app files: %w", err)
	}
	return nil
}

func electrsAppPaths() electrsPaths {
	root := filepath.Join(appsRoot, electrsAppID)
	return electrsPaths{
		Root:        root,
		ComposePath: filepath.Join(root, "docker-compose.yaml"),
		EnvPath:     filepath.Join(root, appmanifest.ElectrsEnvFile),
		CookiePath:  filepath.Join(root, "bitcoin.cookie"),
	}
}

func (s *Server) installElectrsWithOptions(ctx context.Context, opts electrsInstallOptions) error {
	return s.applyElectrs(ctx, opts.ConfirmBitcoinRestart, opts.StorageMount)
}

func (s *Server) startElectrsWithOptions(ctx context.Context, opts electrsInstallOptions) error {
	return s.applyElectrs(ctx, opts.ConfirmBitcoinRestart, opts.StorageMount)
}

func (s *Server) applyElectrs(ctx context.Context, confirmBitcoinRestart bool, storageMount string) error {
	s.updateAppOperationStage(electrsAppID, "validating")
	bitcoinPaths := bitcoinCoreAppPaths()
	if fileExists(bitcoinPaths.ComposePath) && !confirmBitcoinRestart {
		return errElectrsBitcoinRestartConfirmationRequired
	}
	paths := electrsAppPaths()
	if err := os.MkdirAll(paths.Root, 0750); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}
	dataDir := appmanifest.ElectrsDefaultDataDir
	if strings.TrimSpace(storageMount) != "" {
		var err error
		dataDir, err = resolveInstallDataDirFromStorageMount(ctx, electrsAppID, storageMount)
		if err != nil {
			return err
		}
	} else if raw, readErr := os.ReadFile(paths.EnvPath); readErr == nil {
		if existing, parseErr := appmanifest.ParseElectrsRuntimeEnv(raw); parseErr == nil && existing.DataDir != "" {
			dataDir = existing.DataDir
		}
	}
	if handled, err := system.EnsureCatalogStorageWithBroker(ctx, electrsAppID, dataDir, false); !handled {
		return errors.New("Electrs storage requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("Electrs storage unavailable: %w", err)
	}
	if err := ensureDockerForCatalogAppEnforce(ctx); err != nil {
		return err
	}

	s.updateAppOperationStage(electrsAppID, "configuring_bitcoin")
	values, validationConfig, err := s.resolveElectrsRuntimeValues(ctx, bitcoinPaths, confirmBitcoinRestart)
	if err != nil {
		return err
	}
	if availability := fullIndexBitcoinConfigAvailability(ctx, validationConfig); !availability.Available {
		return errors.New(availability.Message)
	}

	// Electrs reads the credential verbatim as HTTP Basic auth, so the private
	// manager-side file is exactly "user:password" with no trailing newline.
	// The broker validates and copies it to the non-root container snapshot.
	s.updateAppOperationStage(electrsAppID, "preparing_image")
	if err := ensureElectrsImage(ctx); err != nil {
		return err
	}
	s.updateAppOperationStage(electrsAppID, "configuring_runtime")
	cookie := values.BitcoinRPCUser + ":" + values.BitcoinRPCPass
	if err := writeFile(paths.CookiePath, cookie, 0600); err != nil {
		return fmt.Errorf("failed to write bitcoin cookie file: %w", err)
	}
	if err := os.Chmod(paths.CookiePath, 0600); err != nil {
		return fmt.Errorf("failed to secure bitcoin cookie file: %w", err)
	}
	runtime := appmanifest.ElectrsRuntime{BitcoinMode: values.BitcoinMode, Network: values.Network, DataDir: dataDir}
	env, err := appmanifest.ElectrsRuntimeEnv(runtime)
	if err != nil {
		return err
	}
	if err := writeFile(paths.EnvPath, env, 0600); err != nil {
		return fmt.Errorf("failed to write Electrs environment: %w", err)
	}
	if err := os.Chmod(paths.EnvPath, 0600); err != nil {
		return fmt.Errorf("failed to secure Electrs environment: %w", err)
	}
	if _, err := ensureFileWithChange(paths.ComposePath, electrsComposeContents(paths, values, dataDir)); err != nil {
		return err
	}
	s.updateAppOperationStage(electrsAppID, "starting_service")
	if handled, err := system.AppLifecycleWithBroker(ctx, electrsAppID, "start"); !handled {
		return errors.New("Electrs lifecycle requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("Electrs start failed: %w", err)
	}
	if _, err := system.EnsureCatalogStorageWithBroker(ctx, electrsAppID, dataDir, true); err != nil {
		return fmt.Errorf("Electrs legacy storage cleanup failed: %w", err)
	}
	return nil
}

func ensureElectrsImage(ctx context.Context) error {
	if handled, err := system.PrepareAppImageWithBroker(ctx, appmanifest.ElectrsID, string(appmanifest.ElectrsImageApp)); handled {
		return err
	}
	return errors.New("Electrs image preparation requires privileged broker enforce mode")
}

func (s *Server) resolveElectrsRuntimeValues(ctx context.Context, bitcoinPaths bitcoinCorePaths, confirmBitcoinRestart bool) (electrsRuntimeValues, bitcoinRPCConfig, error) {
	localCfg, err := s.resolveElectrsBitcoinRPCConfig(ctx, bitcoinPaths, confirmBitcoinRestart)
	if err != nil {
		return electrsRuntimeValues{}, bitcoinRPCConfig{}, err
	}
	if strings.TrimSpace(localCfg.User) == "" || strings.TrimSpace(localCfg.Pass) == "" {
		return electrsRuntimeValues{}, bitcoinRPCConfig{}, errors.New("local bitcoin RPC credentials missing")
	}
	_, rpcPort := parseMainchainRPC(localCfg.Host)
	// /data/bitcoin/bitcoin.conf is root-owned on a deployed host, so read via
	// the same docker-exec ladder readBitcoinCoreConfig uses — direct ReadFile
	// would silently permission-deny and fall back to treating the node as
	// mainnet, misconfiguring electrs.
	info, err := fetchBitcoinInfo(ctx, localCfg.Host, localCfg.User, localCfg.Pass)
	if err != nil {
		return electrsRuntimeValues{}, bitcoinRPCConfig{}, fmt.Errorf("failed to detect local Bitcoin chain: %w", err)
	}
	network, p2pPort := electrsNetworkAndP2PPort(info.Chain)
	networkContract, err := appmanifest.ElectrsNetworkForName(network)
	if err != nil || rpcPort != networkContract.RPCPort || p2pPort != networkContract.P2PPort {
		return electrsRuntimeValues{}, bitcoinRPCConfig{}, errors.New("local Bitcoin uses a non-catalog RPC or P2P port")
	}
	rpcHost := "bitcoind"
	p2pHost := "bitcoind"
	bitcoinMode := appmanifest.ElectrsBitcoinModeApp
	if !fileExists(bitcoinPaths.ComposePath) {
		if !isLocalRPCHost(localCfg.Host) {
			return electrsRuntimeValues{}, bitcoinRPCConfig{}, fmt.Errorf("local bitcoin RPC host is not local: %s", localCfg.Host)
		}
		if err := ensureLocalExternalBitcoinConsumerNetwork(ctx); err != nil {
			return electrsRuntimeValues{}, bitcoinRPCConfig{}, err
		}
		rpcHost = appmanifest.BitcoinConsumerHostGateway
		p2pHost = appmanifest.BitcoinConsumerHostGateway
		bitcoinMode = appmanifest.ElectrsBitcoinModeNative
	}
	return electrsRuntimeValues{
		BitcoinRPCUser: localCfg.User,
		BitcoinRPCPass: localCfg.Pass,
		BitcoinRPCHost: rpcHost,
		BitcoinRPCPort: rpcPort,
		Network:        network,
		BitcoinP2PHost: p2pHost,
		BitcoinP2PPort: p2pPort,
		BitcoinMode:    bitcoinMode,
	}, localCfg, nil
}

func (s *Server) resolveElectrsBitcoinRPCConfig(ctx context.Context, bitcoinPaths bitcoinCorePaths, confirmBitcoinRestart bool) (bitcoinRPCConfig, error) {
	if !fileExists(bitcoinPaths.ComposePath) {
		cfg, err := readBitcoinLocalRPCConfig(ctx)
		if err != nil {
			return bitcoinRPCConfig{}, fmt.Errorf("local bitcoin RPC unavailable: %w", err)
		}
		return cfg, nil
	}
	if !confirmBitcoinRestart {
		return bitcoinRPCConfig{}, errElectrsBitcoinRestartConfirmationRequired
	}
	status, err := inspectBitcoinCoreStatus(ctx)
	if err != nil {
		return bitcoinRPCConfig{}, err
	}
	if status != "running" {
		return bitcoinRPCConfig{}, errors.New("Bitcoin Core must be running before Electrs can be installed or started")
	}
	user, password, credentialStatus, _, handled, err := system.EnsureBitcoinCoreElectrsCredentialsWithBroker(ctx, bitcoinPaths.DataDir)
	if !handled {
		return bitcoinRPCConfig{}, errors.New("Electrs Bitcoin RPC credential migration requires privileged broker enforce mode")
	}
	if err != nil {
		return bitcoinRPCConfig{}, fmt.Errorf("Electrs Bitcoin RPC credential migration failed: %w", err)
	}
	if credentialStatus == "restart_required" {
		if err := runBitcoinCoreLifecycle(ctx, "restart"); err != nil {
			return bitcoinRPCConfig{}, fmt.Errorf("failed to restart Bitcoin Core after Electrs RPC credential migration: %w", err)
		}
		s.invalidateBitcoinStatusCaches()
		user, password, err = s.waitForElectrsBitcoinCredential(ctx, bitcoinPaths.DataDir, user, password)
		if err != nil {
			return bitcoinRPCConfig{}, err
		}
	} else if credentialStatus != "ready" {
		return bitcoinRPCConfig{}, errors.New("Electrs Bitcoin RPC credential returned an invalid state")
	}
	return bitcoinRPCConfig{Host: "127.0.0.1:8332", User: user, Pass: password}, nil
}

func (s *Server) waitForElectrsBitcoinCredential(ctx context.Context, dataDir string, expectedUser string, expectedPassword string) (string, string, error) {
	deadline := time.Now().Add(electrsCredentialActivationTimeout)
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		user, password, status, changed, handled, err := system.EnsureBitcoinCoreElectrsCredentialsWithBroker(attemptCtx, dataDir)
		cancel()
		if !handled {
			return "", "", errors.New("Electrs Bitcoin RPC credential activation requires privileged broker enforce mode")
		}
		if err == nil && status == "ready" && !changed {
			if user != expectedUser || password != expectedPassword {
				return "", "", errors.New("Electrs Bitcoin RPC credential changed during activation")
			}
			return user, password, nil
		}
		if time.Now().After(deadline) {
			return "", "", errors.New("Bitcoin Core did not activate the dedicated Electrs RPC credential before the timeout")
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", "", ctx.Err()
		case <-timer.C:
		}
	}
}

func electrsNetworkAndP2PPort(chain string) (string, int) {
	switch strings.ToLower(strings.TrimSpace(chain)) {
	case "test", "testnet", "testnet3", "testnet4":
		return "testnet", 18333
	case "signet":
		return "signet", 38333
	case "regtest":
		return "regtest", 18444
	default:
		return "bitcoin", 8333
	}
}

// detectBitcoinCoreChain scans a bitcoin.conf body for a top-level chain
// selector (testnet/signet/regtest) and returns the electrs --network value
// plus the default bitcoind P2P port for that chain. Only top-level keys
// (above any [section] header) are considered, matching how bitcoind itself
// interprets chain selectors.
func detectBitcoinCoreChain(raw string) (string, int) {
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			break
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if value != "1" {
			continue
		}
		switch key {
		case "testnet":
			return "testnet", 18333
		case "signet":
			return "signet", 38333
		case "regtest":
			return "regtest", 18444
		}
	}
	return "bitcoin", 8333
}

// electrsComposeContents delegates to the closed catalog. The RocksDB index
// lives in a Docker named volume whose mount point is owned by the fixed
// non-root UID in the broker-built image; no manager-selected path reaches
// Docker.
func electrsComposeContents(_ electrsPaths, values electrsRuntimeValues, dataDirs ...string) string {
	dataDir := ""
	if len(dataDirs) > 0 {
		dataDir = dataDirs[0]
	}
	compose, err := appmanifest.ElectrsCompose(appmanifest.ElectrsRuntime{
		BitcoinMode: values.BitcoinMode,
		Network:     values.Network,
		DataDir:     dataDir,
	})
	if err != nil {
		return ""
	}
	return compose
}

type electrsStatus struct {
	Installed   bool   `json:"installed"`
	Running     bool   `json:"running"`
	RPCPort     int    `json:"rpc_port"`
	IndexHeight int64  `json:"index_height"`
	TipHeight   int64  `json:"tip_height"`
	Indexing    bool   `json:"indexing"`
	Message     string `json:"message,omitempty"`
}

// fetchElectrsStatus scrapes electrs' Prometheus endpoint on 127.0.0.1:4224
// for the indexed tip (electrs_index_height{type="tip"}) and pulls the
// bitcoind chain tip from the local Bitcoin Core app (if installed) so the
// UI can show indexing progress.
func (s *Server) fetchElectrsStatus(ctx context.Context) electrsStatus {
	out := electrsStatus{RPCPort: electrsRPCPort}

	paths := electrsAppPaths()
	if !fileExists(paths.ComposePath) {
		out.Message = "not installed"
		return out
	}
	out.Installed = true

	handled, composeStatus, _, err := system.InspectAppWithBroker(ctx, electrsAppID)
	if handled && err == nil && composeStatus == "running" {
		out.Running = true
	}
	if !handled {
		out.Message = "status requires privileged broker enforce mode"
		return out
	}
	if !out.Running {
		out.Message = "container not running"
		return out
	}

	indexHeight, scrapeErr := scrapeElectrsIndexHeight(ctx)
	if scrapeErr != nil {
		out.Message = fmt.Sprintf("metrics unreachable: %v", scrapeErr)
	} else {
		out.IndexHeight = indexHeight
	}

	chainCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if cfg, err := readBitcoinLocalRPCConfig(chainCtx); err == nil {
		if info, err := fetchBitcoinInfo(chainCtx, cfg.Host, cfg.User, cfg.Pass); err == nil {
			out.TipHeight = info.Blocks
		} else if out.Message == "" {
			out.Message = "Bitcoin RPC status unavailable; synchronization is unknown"
		}
	} else if out.Message == "" {
		out.Message = "Bitcoin RPC credentials unavailable; synchronization is unknown"
	}

	if out.IndexHeight > 0 && out.TipHeight > 0 {
		out.Indexing = out.IndexHeight < out.TipHeight-1
	}
	return out
}

func scrapeElectrsIndexHeight(ctx context.Context) (int64, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/metrics", electrsMonitorPort), nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return 0, fmt.Errorf("metrics http %d", resp.StatusCode)
	}
	return parseElectrsIndexHeight(resp.Body)
}

// parseElectrsIndexHeight scans a Prometheus exposition stream for
// electrs_index_height{type="tip"} and returns its value. electrs also
// exposes type="committed" and type="best" — we only want "tip".
func parseElectrsIndexHeight(r io.Reader) (int64, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		if !strings.HasPrefix(line, "electrs_index_height") {
			continue
		}
		braceOpen := strings.IndexByte(line, '{')
		braceClose := strings.IndexByte(line, '}')
		if braceOpen < 0 || braceClose < 0 || braceClose < braceOpen {
			continue
		}
		labels := line[braceOpen+1 : braceClose]
		if !strings.Contains(labels, `type="tip"`) {
			continue
		}
		rest := strings.TrimSpace(line[braceClose+1:])
		if rest == "" {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			return 0, err
		}
		return int64(v), nil
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, errors.New(`metric electrs_index_height{type="tip"} not found`)
}
