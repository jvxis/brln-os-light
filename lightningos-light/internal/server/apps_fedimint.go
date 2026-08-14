package server

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/lndclient"
	"lightningos-light/internal/system"

	"golang.org/x/crypto/bcrypt"
)

const (
	fedimintLegacyAppID   = "fedimint"
	fedimintGuardianAppID = appmanifest.FedimintGuardianID
	fedimintGatewayAppID  = appmanifest.FedimintGatewayID
)

var errFedimintLogServiceNotInstalled = errors.New("Fedimint app is not installed")
var errFedimintGatewayBitcoinRestartConfirmationRequired = errors.New("confirm the one-time Bitcoin Core restart required to enable wallet RPC for Fedimint Gateway")

type fedimintGatewayStartOptions struct {
	ConfirmBitcoinRestart bool `json:"confirm_bitcoin_restart"`
}

type fedimintGuardianPaths struct {
	Root        string
	DataRoot    string
	DataDir     string
	ComposePath string
	RuntimePath string
}

type fedimintGatewayPaths struct {
	Root              string
	DataRoot          string
	DataDir           string
	ComposePath       string
	RuntimePath       string
	TLSCertPath       string
	MacaroonPath      string
	AdminPasswordPath string
	PasswordHashPath  string
}

type fedimintGuardianApp struct{ server *Server }
type fedimintGatewayApp struct{ server *Server }

func newFedimintGuardianApp(s *Server) appHandler { return fedimintGuardianApp{server: s} }
func newFedimintGatewayApp(s *Server) appHandler  { return fedimintGatewayApp{server: s} }

func fedimintGuardianDefinition() appDefinition {
	return appDefinition{
		ID: appmanifest.FedimintGuardianID, Name: "Fedimint Guardian",
		Description: "Run a Fedimint guardian for a solo or multi-guardian federation over Iroh.",
		Port:        appmanifest.FedimintGuardianUIPort,
	}
}

func fedimintGatewayDefinition() appDefinition {
	return appDefinition{
		ID: appmanifest.FedimintGatewayID, Name: "Fedimint Lightning Gateway",
		Description: "Connect your local LND to Fedimint federations as an independent Lightning gateway.",
		Port:        appmanifest.FedimintGatewayUIPort,
		SecurityNotices: []string{
			appSecurityNoticeElevatedLNDAccess,
		},
	}
}

func (a fedimintGuardianApp) Definition() appDefinition { return fedimintGuardianDefinition() }
func (a fedimintGatewayApp) Definition() appDefinition  { return fedimintGatewayDefinition() }

func fedimintBrokerInfo(ctx context.Context, definition appDefinition, composePath string, adminPath string) (appInfo, error) {
	info := newAppInfo(definition)
	if !fileExists(composePath) {
		return info, nil
	}
	info.Installed = true
	info.AdminPasswordPath = adminPath
	handled, status, _, err := system.InspectAppWithBroker(ctx, definition.ID)
	if !handled {
		info.Status = "unknown"
		return info, errors.New("Fedimint status requires privileged broker enforce mode")
	}
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func (a fedimintGuardianApp) Info(ctx context.Context) (appInfo, error) {
	return fedimintBrokerInfo(ctx, a.Definition(), fedimintGuardianAppPaths().ComposePath, "")
}

func (a fedimintGatewayApp) Info(ctx context.Context) (appInfo, error) {
	paths := fedimintGatewayAppPaths()
	return fedimintBrokerInfo(ctx, a.Definition(), paths.ComposePath, paths.AdminPasswordPath)
}

func (a fedimintGuardianApp) Install(ctx context.Context) error {
	return a.server.applyFedimintGuardian(ctx)
}
func (a fedimintGuardianApp) Start(ctx context.Context) error {
	return a.server.applyFedimintGuardian(ctx)
}
func (a fedimintGatewayApp) Install(ctx context.Context) error {
	return a.server.applyFedimintGateway(ctx, false)
}
func (a fedimintGatewayApp) Start(ctx context.Context) error {
	return a.server.applyFedimintGateway(ctx, false)
}

func (a fedimintGuardianApp) Stop(ctx context.Context) error {
	return stopFedimintApp(ctx, appmanifest.FedimintGuardianID, fedimintGuardianAppPaths().ComposePath)
}

func (a fedimintGatewayApp) Stop(ctx context.Context) error {
	return stopFedimintApp(ctx, appmanifest.FedimintGatewayID, fedimintGatewayAppPaths().ComposePath)
}

func stopFedimintApp(ctx context.Context, appID string, composePath string) error {
	if !fileExists(composePath) {
		return errors.New("Fedimint app is not installed")
	}
	if handled, err := system.AppLifecycleWithBroker(ctx, appID, "stop"); !handled {
		return errors.New("Fedimint lifecycle requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("Fedimint stop failed: %w", err)
	}
	return nil
}

func (a fedimintGuardianApp) Uninstall(ctx context.Context) error {
	return uninstallFedimintApp(ctx, appmanifest.FedimintGuardianID, fedimintGuardianAppPaths().Root, fedimintGuardianAppPaths().ComposePath)
}

func (a fedimintGatewayApp) Uninstall(ctx context.Context) error {
	return uninstallFedimintApp(ctx, appmanifest.FedimintGatewayID, fedimintGatewayAppPaths().Root, fedimintGatewayAppPaths().ComposePath)
}

func uninstallFedimintApp(ctx context.Context, appID string, root string, composePath string) error {
	if fileExists(composePath) {
		if handled, err := system.RemoveAppWithBroker(ctx, appID); !handled {
			return errors.New("Fedimint removal requires privileged broker enforce mode")
		} else if err != nil {
			return fmt.Errorf("Fedimint removal failed: %w", err)
		}
	}
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("failed to remove Fedimint declaration: %w", err)
	}
	return nil
}

func fedimintGuardianAppPaths() fedimintGuardianPaths {
	root := filepath.Join(appsRoot, appmanifest.FedimintGuardianID)
	dataRoot := filepath.Join(appsDataRoot, appmanifest.FedimintGuardianID)
	return fedimintGuardianPaths{
		Root: root, DataRoot: dataRoot, DataDir: filepath.Join(dataRoot, "fedimintd"),
		ComposePath: filepath.Join(root, appmanifest.FedimintGuardianComposeFile),
		RuntimePath: filepath.Join(root, appmanifest.FedimintGuardianRuntimeFile),
	}
}

func fedimintGatewayAppPaths() fedimintGatewayPaths {
	root := filepath.Join(appsRoot, appmanifest.FedimintGatewayID)
	dataRoot := filepath.Join(appsDataRoot, appmanifest.FedimintGatewayID)
	return fedimintGatewayPaths{
		Root: root, DataRoot: dataRoot, DataDir: filepath.Join(dataRoot, "gatewayd"),
		ComposePath:       filepath.Join(root, appmanifest.FedimintGatewayComposeFile),
		RuntimePath:       filepath.Join(root, appmanifest.FedimintGatewayRuntimeFile),
		TLSCertPath:       filepath.Join(root, appmanifest.FedimintGatewayTLSFile),
		MacaroonPath:      filepath.Join(root, appmanifest.FedimintGatewayMacaroonFile),
		AdminPasswordPath: filepath.Join(dataRoot, "gateway-admin.txt"),
		PasswordHashPath:  filepath.Join(dataRoot, "gateway-password-hash.txt"),
	}
}

func (s *Server) applyFedimintGuardian(ctx context.Context) error {
	if err := ensureDockerForCatalogAppEnforce(ctx); err != nil {
		return err
	}
	paths := fedimintGuardianAppPaths()
	if err := os.MkdirAll(paths.Root, 0750); err != nil {
		return fmt.Errorf("failed to create Guardian declaration: %w", err)
	}
	bitcoin, err := s.resolveFedimintBitcoinRuntime(ctx, "Fedimint Guardian")
	if err != nil {
		return err
	}
	runtime := appmanifest.FedimintGuardianRuntime{Bitcoin: bitcoin}
	if err := writeFedimintDeclaration(paths.RuntimePath, paths.ComposePath, runtime); err != nil {
		return err
	}
	if err := prepareAndProbeFedimintImage(ctx, appmanifest.FedimintGuardianID); err != nil {
		return err
	}
	return startFedimintWithBroker(ctx, appmanifest.FedimintGuardianID, s.logger)
}

func (s *Server) installFedimintGatewayWithOptions(ctx context.Context, opts fedimintGatewayStartOptions) error {
	return s.applyFedimintGateway(ctx, opts.ConfirmBitcoinRestart)
}

func (s *Server) startFedimintGatewayWithOptions(ctx context.Context, opts fedimintGatewayStartOptions) error {
	return s.applyFedimintGateway(ctx, opts.ConfirmBitcoinRestart)
}

func (s *Server) applyFedimintGateway(ctx context.Context, confirmBitcoinRestart bool) error {
	if err := ensureDockerForCatalogAppEnforce(ctx); err != nil {
		return err
	}
	if err := s.ensureFedimintGatewayBitcoinWalletRPC(ctx, confirmBitcoinRestart); err != nil {
		return err
	}
	paths := fedimintGatewayAppPaths()
	for _, dir := range []string{paths.Root, paths.DataRoot} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("failed to create Gateway declaration: %w", err)
		}
	}
	if err := migrateLegacyFedimintGatewaySecrets(paths); err != nil {
		return err
	}
	_, passwordHash, err := ensureFedimintGatewayCredentials(paths)
	if err != nil {
		return err
	}
	if err := s.ensureFedimintGatewayLNDMaterial(ctx, paths); err != nil {
		return err
	}
	bitcoin, err := s.resolveFedimintBitcoinRuntime(ctx, "Fedimint Lightning Gateway")
	if err != nil {
		return err
	}
	runtime := appmanifest.FedimintGatewayRuntime{Bitcoin: bitcoin, GatewayPasswordHash: passwordHash}
	if err := writeFedimintDeclaration(paths.RuntimePath, paths.ComposePath, runtime); err != nil {
		return err
	}
	if err := prepareAndProbeFedimintImage(ctx, appmanifest.FedimintGatewayID); err != nil {
		return err
	}
	if err := startFedimintWithBroker(ctx, appmanifest.FedimintGatewayID, s.logger); err != nil {
		return fedimintGatewayStartupError(err)
	}
	return nil
}

func (s *Server) ensureFedimintGatewayBitcoinWalletRPC(ctx context.Context, confirmBitcoinRestart bool) error {
	if readBitcoinSource() == "remote" {
		return nil
	}
	paths := bitcoinCoreAppPaths()
	if !fileExists(paths.ComposePath) {
		// Existing/systemd Bitcoin belongs to the operator. Gateway startup will
		// report the upstream wallet-RPC requirement without modifying the node.
		return nil
	}
	raw, err := readBitcoinCoreConfigRaw(ctx, paths)
	if err != nil {
		return err
	}
	updated, changed := enableBitcoinCoreWalletRPCForGateway(raw)
	if changed {
		status, err := inspectBitcoinCoreStatus(ctx)
		if err != nil {
			return err
		}
		if status != "running" {
			return errors.New("Bitcoin Core must be running before Fedimint Gateway can enable wallet RPC")
		}
		if !confirmBitcoinRestart {
			return errFedimintGatewayBitcoinRestartConfirmationRequired
		}
		if err := writeBitcoinCoreConfig(ctx, paths, updated); err != nil {
			return fmt.Errorf("failed to enable Bitcoin Core wallet RPC for Fedimint Gateway: %w", err)
		}
		if err := runBitcoinCoreLifecycle(ctx, "restart"); err != nil {
			if rollbackErr := writeBitcoinCoreConfig(ctx, paths, raw); rollbackErr != nil {
				return fmt.Errorf("failed to restart Bitcoin Core after enabling Fedimint Gateway wallet RPC: %w (configuration rollback also failed: %v)", err, rollbackErr)
			}
			return fmt.Errorf("failed to restart Bitcoin Core after enabling Fedimint Gateway wallet RPC: %w", err)
		}
		s.invalidateBitcoinStatusCaches()
	}
	cfg, err := readBitcoinLocalRPCConfig(ctx)
	if err != nil {
		return fmt.Errorf("Bitcoin Core RPC credentials unavailable after Fedimint Gateway migration: %w", err)
	}
	if changed {
		if err := waitForBitcoinRPC(ctx, cfg, 2*time.Minute); err != nil {
			return err
		}
	}
	if _, err := fetchBitcoinRPC(ctx, cfg.Host, cfg.User, cfg.Pass, "listwallets"); err != nil {
		return fmt.Errorf("Bitcoin Core wallet RPC is unavailable after the Fedimint Gateway migration: %w", err)
	}
	return nil
}

func migrateLegacyFedimintGatewaySecrets(paths fedimintGatewayPaths) error {
	legacyRoot := filepath.Join(appsDataRoot, fedimintLegacyAppID)
	for _, pair := range [][2]string{
		{filepath.Join(legacyRoot, "gateway-admin.txt"), paths.AdminPasswordPath},
		{filepath.Join(legacyRoot, "gateway-password-hash.txt"), paths.PasswordHashPath},
	} {
		if fileExists(pair[1]) || !fileExists(pair[0]) {
			continue
		}
		raw, err := os.ReadFile(pair[0])
		if err != nil || len(raw) == 0 {
			return errors.New("legacy Fedimint Gateway credential is unavailable")
		}
		if err := writeFile(pair[1], string(raw), 0600); err != nil {
			return errors.New("failed to preserve legacy Fedimint Gateway credential")
		}
	}
	return nil
}

func writeFedimintDeclaration(runtimePath string, composePath string, runtime any) error {
	var runtimeRaw []byte
	var compose string
	var err error
	switch value := runtime.(type) {
	case appmanifest.FedimintGuardianRuntime:
		runtimeRaw, err = appmanifest.FedimintGuardianRuntimeJSON(value)
		if err == nil {
			compose, err = appmanifest.FedimintGuardianCompose(value)
		}
	case appmanifest.FedimintGatewayRuntime:
		runtimeRaw, err = appmanifest.FedimintGatewayRuntimeJSON(value)
		if err == nil {
			compose, err = appmanifest.FedimintGatewayCompose(value)
		}
	default:
		err = errors.New("Fedimint runtime type is invalid")
	}
	if err != nil {
		return err
	}
	if err := writeFile(runtimePath, string(runtimeRaw), 0600); err != nil {
		return fmt.Errorf("failed to write Fedimint runtime: %w", err)
	}
	if err := os.Chmod(runtimePath, 0600); err != nil {
		return fmt.Errorf("failed to secure Fedimint runtime: %w", err)
	}
	if _, err := ensureFileWithChange(composePath, compose); err != nil {
		return err
	}
	if err := os.Chmod(composePath, 0600); err != nil {
		return fmt.Errorf("failed to secure Fedimint compose declaration: %w", err)
	}
	return nil
}

func prepareAndProbeFedimintImage(ctx context.Context, appID string) error {
	if handled, err := system.PrepareAppImageWithBroker(ctx, appID, string(appmanifest.FedimintImageApp)); !handled {
		return errors.New("Fedimint image preparation requires privileged broker enforce mode")
	} else if err != nil {
		return err
	}
	if handled, runnable, err := system.ProbeAppImageWithBroker(ctx, appID, string(appmanifest.FedimintImageApp)); !handled {
		return errors.New("Fedimint image probe requires privileged broker enforce mode")
	} else if err != nil {
		return err
	} else if !runnable {
		return errors.New("Fedimint image failed the closed runtime probe")
	}
	return nil
}

func startFedimintWithBroker(ctx context.Context, appID string, logger interface{ Printf(string, ...any) }) error {
	if handled, err := system.AppLifecycleWithBroker(ctx, appID, "start"); !handled {
		return errors.New("Fedimint lifecycle requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("Fedimint start failed: %w", err)
	}
	if handled, _, err := system.EnsureAppFirewallWithBroker(ctx, appID); !handled {
		return errors.New("Fedimint firewall requires privileged broker enforce mode")
	} else if err != nil && logger != nil {
		logger.Printf("%s: broker firewall rule failed: %v", appID, err)
	}
	return waitForFedimintAppStable(ctx, appID)
}

func waitForFedimintAppStable(ctx context.Context, appID string) error {
	return waitForFedimintAppStableWithPolicy(ctx, appID, time.Second, 8, 12)
}

func waitForFedimintAppStableWithPolicy(ctx context.Context, appID string, interval time.Duration, stableChecks, maxChecks int) error {
	if stableChecks < 1 || maxChecks < stableChecks {
		return errors.New("invalid Fedimint startup stability policy")
	}
	consecutiveRunning := 0
	lastStatus := "unknown"
	var lastErr error
	for check := 0; check < maxChecks; check++ {
		handled, status, _, err := system.InspectAppWithBroker(ctx, appID)
		if !handled {
			return errors.New("Fedimint startup status requires privileged broker enforce mode")
		}
		if err != nil {
			lastErr = err
			consecutiveRunning = 0
		} else {
			lastStatus = status
			if status == "running" {
				consecutiveRunning++
				if consecutiveRunning >= stableChecks {
					return nil
				}
			} else {
				consecutiveRunning = 0
			}
		}
		if check == maxChecks-1 {
			break
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	if lastErr != nil {
		return fmt.Errorf("Fedimint service status could not be confirmed: %w", lastErr)
	}
	return fmt.Errorf("Fedimint service did not remain active (last status: %s)", lastStatus)
}

func (s *Server) resolveFedimintBitcoinRuntime(ctx context.Context, appName string) (appmanifest.FedimintBitcoinRuntime, error) {
	var cfg bitcoinRPCConfig
	mode := appmanifest.FedimintBitcoinModeRemote
	if readBitcoinSource() == "remote" {
		var ok bool
		cfg, ok = readBitcoindRPCConfigFromLNDConf()
		if !ok {
			return appmanifest.FedimintBitcoinRuntime{}, fmt.Errorf("bitcoin RPC credentials unavailable: configure bitcoind.rpchost, bitcoind.rpcuser and bitcoind.rpcpass in %s before starting %s", lndConfPath, appName)
		}
	} else {
		var err error
		cfg, err = readBitcoinLocalRPCConfig(ctx)
		if err != nil {
			return appmanifest.FedimintBitcoinRuntime{}, fmt.Errorf("bitcoin RPC credentials unavailable before starting %s: %w", appName, err)
		}
		mode = appmanifest.FedimintBitcoinModeApp
		if !fileExists(bitcoinCoreAppPaths().ComposePath) {
			if !isLocalRPCHost(cfg.Host) {
				return appmanifest.FedimintBitcoinRuntime{}, fmt.Errorf("local bitcoin RPC host is not local: %s", cfg.Host)
			}
			if err := ensureLocalExternalBitcoinConsumerNetwork(ctx); err != nil {
				return appmanifest.FedimintBitcoinRuntime{}, err
			}
			mode = appmanifest.FedimintBitcoinModeNative
		}
	}
	host, port := parseMainchainRPC(cfg.Host)
	if mode == appmanifest.FedimintBitcoinModeApp {
		host = "bitcoind"
	} else if mode == appmanifest.FedimintBitcoinModeNative {
		host = appmanifest.BitcoinConsumerHostGateway
	}
	runtime := appmanifest.FedimintBitcoinRuntime{
		Mode: mode, URL: "http://" + net.JoinHostPort(host, strconv.Itoa(port)), User: cfg.User, Pass: cfg.Pass,
	}
	if err := appmanifest.ValidateFedimintBitcoinRuntime(runtime); err != nil {
		return appmanifest.FedimintBitcoinRuntime{}, err
	}
	return runtime, nil
}

func ensureFedimintGatewayCredentials(paths fedimintGatewayPaths) (string, string, error) {
	password := readSecretFile(paths.AdminPasswordPath)
	if password == "" {
		var err error
		password, err = randomToken(24)
		if err != nil {
			return "", "", fmt.Errorf("failed to generate gateway password: %w", err)
		}
		if err := writeFile(paths.AdminPasswordPath, password+"\n", 0600); err != nil {
			return "", "", err
		}
	}
	hash := readSecretFile(paths.PasswordHashPath)
	if hash == "" {
		rawHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return "", "", fmt.Errorf("failed to hash gateway password: %w", err)
		}
		hash = string(rawHash)
		if err := writeFile(paths.PasswordHashPath, hash+"\n", 0600); err != nil {
			return "", "", err
		}
	}
	return password, hash, nil
}

func (s *Server) ensureFedimintGatewayLNDMaterial(ctx context.Context, paths fedimintGatewayPaths) error {
	certificate, err := os.ReadFile("/data/lnd/tls.cert")
	if err != nil || len(certificate) == 0 {
		return errors.New("LND TLS certificate is unavailable")
	}
	if err := writeFile(paths.TLSCertPath, string(certificate), 0600); err != nil {
		return fmt.Errorf("failed to stage Gateway LND certificate: %w", err)
	}
	if info, err := os.Lstat(paths.MacaroonPath); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Gateway LND credential must be a regular file")
		}
		credential, err := os.ReadFile(paths.MacaroonPath)
		if err != nil || len(credential) == 0 {
			return errors.New("Gateway LND credential is unavailable")
		}
		if err := validateFedimintGatewayCredentialNotAdmin(credential); err != nil {
			return err
		}
		return os.Chmod(paths.MacaroonPath, 0600)
	} else if !os.IsNotExist(err) {
		return errors.New("Gateway LND credential is unavailable")
	}
	if s.lnd == nil {
		return errors.New("LND client unavailable")
	}
	ids, err := s.lnd.ListMacaroonIDs(ctx)
	if err != nil {
		return fmt.Errorf("failed to list LND macaroon IDs: %w", err)
	}
	rootKeyID, err := lndclient.GenerateMacaroonRootKeyID(ids, time.Now())
	if err != nil {
		return err
	}
	result, err := s.lnd.BakeCustomMacaroon(ctx, lndclient.BakeCustomMacaroonRequest{
		Permissions: fedimintGatewayMacaroonPermissions(), RootKeyID: rootKeyID,
	})
	if err != nil {
		return fmt.Errorf("failed to bake Gateway macaroon: %w", err)
	}
	raw, err := hex.DecodeString(result.MacaroonHex)
	if err != nil || len(raw) == 0 {
		return errors.New("invalid LND macaroon response")
	}
	if err := validateFedimintGatewayCredentialNotAdmin(raw); err != nil {
		return err
	}
	file, err := os.OpenFile(paths.MacaroonPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("failed to write Gateway macaroon: %w", err)
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return fmt.Errorf("failed to write Gateway macaroon: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close Gateway macaroon: %w", err)
	}
	return nil
}

func validateFedimintGatewayCredentialNotAdmin(credential []byte) error {
	equal, err := lndCredentialEqualsNativeAdmin(credential)
	if err != nil {
		return err
	}
	if equal {
		return errors.New("Gateway LND credential must not be the admin macaroon")
	}
	return nil
}

func fedimintGatewayMacaroonPermissions() []lndclient.MacaroonPermission {
	return []lndclient.MacaroonPermission{
		{Entity: "info", Action: "read"},
		{Entity: "invoices", Action: "read"},
		{Entity: "invoices", Action: "write"},
		{Entity: "offchain", Action: "read"},
		{Entity: "offchain", Action: "write"},
		{Entity: "onchain", Action: "read"},
		{Entity: "onchain", Action: "write"},
		{Entity: "peers", Action: "read"},
		{Entity: "peers", Action: "write"},
	}
}

func fedimintGatewayStartupError(err error) error {
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(err.Error())
	if strings.Contains(strings.ToLower(detail), "method not found") {
		return errors.New("Fedimint Lightning Gateway requires Bitcoin Core wallet RPC methods (including createwallet), but the configured Bitcoin RPC endpoint rejected that method")
	}
	return fmt.Errorf("Fedimint Lightning Gateway failed startup validation: %w", err)
}

func isFedimintLogService(service string) bool {
	switch strings.ToLower(strings.TrimSpace(service)) {
	case appmanifest.FedimintGuardianID, "fedimintd", appmanifest.FedimintGatewayID, "gatewayd", "fedimint-lightning-gateway":
		return true
	default:
		return false
	}
}

func readFedimintComposeLogLines(ctx context.Context, service string, lines int, since string) ([]string, string, error) {
	if lines < 1 {
		lines = 200
	}
	if lines > 500 {
		lines = 500
	}
	appID := ""
	switch strings.ToLower(strings.TrimSpace(service)) {
	case appmanifest.FedimintGuardianID, "fedimintd":
		if !fileExists(fedimintGuardianAppPaths().ComposePath) {
			return nil, "", errFedimintLogServiceNotInstalled
		}
		appID = appmanifest.FedimintGuardianID
	case appmanifest.FedimintGatewayID, "gatewayd", "fedimint-lightning-gateway":
		if !fileExists(fedimintGatewayAppPaths().ComposePath) {
			return nil, "", errFedimintLogServiceNotInstalled
		}
		appID = appmanifest.FedimintGatewayID
	default:
		return nil, "", errFedimintLogServiceNotInstalled
	}
	if handled, result, source, err := system.ReadAppLogsWithBroker(ctx, appID, lines, since); handled {
		return result, source, err
	}
	return nil, "", errors.New("Fedimint logs require privileged broker enforce mode")
}
