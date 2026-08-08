package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/config"
	"lightningos-light/internal/system"

	"golang.org/x/crypto/bcrypt"
)

const (
	fedimintLegacyAppID      = "fedimint"
	fedimintGuardianAppID    = "fedimint-guardian"
	fedimintGatewayAppID     = "fedimint-gateway"
	fedimintdImage           = "fedimint/fedimintd:v0.11.1"
	fedimintGatewayImage     = "fedimint/gatewayd:v0.11.1"
	fedimintP2PPort          = 8173
	fedimintAPIPort          = 8174
	fedimintUIPort           = 8175
	fedimintGatewayUIPort    = 8176
	fedimintGatewayIrohPort  = 8177
	fedimintLndTLSCertPath   = "/data/lnd/tls.cert"
	fedimintGatewayNetwork   = "fedimint-gateway_default"
	fedimintGuardianNetwork  = "fedimint-guardian_default"
	fedimintDockerBridgeName = "docker0"
	fedimintUfwRetries       = 5
)

var errFedimintLogServiceNotInstalled = errors.New("Fedimint app is not installed")

type fedimintGuardianPaths struct {
	Root        string
	DataRoot    string
	DataDir     string
	ComposePath string
}

type fedimintGatewayPaths struct {
	Root              string
	DataRoot          string
	DataDir           string
	ComposePath       string
	AdminPasswordPath string
	PasswordHashPath  string
}

type fedimintLegacyPaths struct {
	Root                     string
	DataRoot                 string
	FedimintDataDir          string
	GatewayDataDir           string
	ComposePath              string
	GatewayAdminPasswordPath string
	GatewayPasswordHashPath  string
}

type fedimintGatewayRuntimeValues struct {
	GatewayPasswordHash string
	LndRPCAddr          string
	LndTLSCertPath      string
	LndMacaroonPath     string
	BitcoinBackend      fedimintBitcoinBackendValues
}

type fedimintBitcoinBackendValues struct {
	URL                      string
	User                     string
	Pass                     string
	UseBitcoinCoreNetwork    bool
	NeedsLocalRPCBridgeUFW   bool
	LocalExternalBitcoinPort int
}

type fedimintGuardianApp struct {
	server *Server
}

type fedimintGatewayApp struct {
	server *Server
}

func newFedimintGuardianApp(s *Server) appHandler {
	return fedimintGuardianApp{server: s}
}

func newFedimintGatewayApp(s *Server) appHandler {
	return fedimintGatewayApp{server: s}
}

func fedimintGuardianDefinition() appDefinition {
	return appDefinition{
		ID:          fedimintGuardianAppID,
		Name:        "Fedimint Guardian",
		Description: "Run a Fedimint guardian for a solo or multi-guardian federation over Iroh.",
		Port:        fedimintUIPort,
	}
}

func fedimintGatewayDefinition() appDefinition {
	return appDefinition{
		ID:          fedimintGatewayAppID,
		Name:        "Fedimint Lightning Gateway",
		Description: "Connect your local LND to Fedimint federations as an independent Lightning gateway.",
		Port:        fedimintGatewayUIPort,
		SecurityNotices: []string{
			appSecurityNoticeElevatedLNDAccess,
		},
	}
}

func (a fedimintGuardianApp) Definition() appDefinition {
	return fedimintGuardianDefinition()
}

func (a fedimintGatewayApp) Definition() appDefinition {
	return fedimintGatewayDefinition()
}

func (a fedimintGuardianApp) Info(ctx context.Context) (appInfo, error) {
	def := a.Definition()
	info := newAppInfo(def)
	paths := fedimintGuardianAppPaths()
	if !fileExists(paths.ComposePath) {
		return info, nil
	}
	info.Installed = true
	status, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, "fedimintd")
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func (a fedimintGatewayApp) Info(ctx context.Context) (appInfo, error) {
	def := a.Definition()
	info := newAppInfo(def)
	paths := fedimintGatewayAppPaths()
	if !fileExists(paths.ComposePath) {
		return info, nil
	}
	info.Installed = true
	info.AdminPasswordPath = paths.AdminPasswordPath
	status, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, "gatewayd")
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func (a fedimintGuardianApp) Install(ctx context.Context) error {
	return a.server.installFedimintGuardian(ctx)
}

func (a fedimintGatewayApp) Install(ctx context.Context) error {
	return a.server.installFedimintGateway(ctx)
}

func (a fedimintGuardianApp) Uninstall(ctx context.Context) error {
	return a.server.uninstallFedimintGuardian(ctx)
}

func (a fedimintGatewayApp) Uninstall(ctx context.Context) error {
	return a.server.uninstallFedimintGateway(ctx)
}

func (a fedimintGuardianApp) Start(ctx context.Context) error {
	return a.server.startFedimintGuardian(ctx)
}

func (a fedimintGatewayApp) Start(ctx context.Context) error {
	return a.server.startFedimintGateway(ctx)
}

func (a fedimintGuardianApp) Stop(ctx context.Context) error {
	return a.server.stopFedimintGuardian(ctx)
}

func (a fedimintGatewayApp) Stop(ctx context.Context) error {
	return a.server.stopFedimintGateway(ctx)
}

func fedimintGuardianAppPaths() fedimintGuardianPaths {
	root := filepath.Join(appsRoot, fedimintGuardianAppID)
	dataRoot := filepath.Join(appsDataRoot, fedimintGuardianAppID)
	return fedimintGuardianPaths{
		Root:        root,
		DataRoot:    dataRoot,
		DataDir:     filepath.Join(dataRoot, "fedimintd"),
		ComposePath: filepath.Join(root, "docker-compose.yaml"),
	}
}

func fedimintGatewayAppPaths() fedimintGatewayPaths {
	root := filepath.Join(appsRoot, fedimintGatewayAppID)
	dataRoot := filepath.Join(appsDataRoot, fedimintGatewayAppID)
	return fedimintGatewayPaths{
		Root:              root,
		DataRoot:          dataRoot,
		DataDir:           filepath.Join(dataRoot, "gatewayd"),
		ComposePath:       filepath.Join(root, "docker-compose.yaml"),
		AdminPasswordPath: filepath.Join(dataRoot, "gateway-admin.txt"),
		PasswordHashPath:  filepath.Join(dataRoot, "gateway-password-hash.txt"),
	}
}

func fedimintLegacyAppPaths() fedimintLegacyPaths {
	root := filepath.Join(appsRoot, fedimintLegacyAppID)
	dataRoot := filepath.Join(appsDataRoot, fedimintLegacyAppID)
	return fedimintLegacyPaths{
		Root:                     root,
		DataRoot:                 dataRoot,
		FedimintDataDir:          filepath.Join(dataRoot, "fedimintd"),
		GatewayDataDir:           filepath.Join(dataRoot, "gatewayd"),
		ComposePath:              filepath.Join(root, "docker-compose.yaml"),
		GatewayAdminPasswordPath: filepath.Join(dataRoot, "gateway-admin.txt"),
		GatewayPasswordHashPath:  filepath.Join(dataRoot, "gateway-password-hash.txt"),
	}
}

func (s *Server) installFedimintGuardian(ctx context.Context) error {
	return s.startFedimintGuardian(ctx)
}

func (s *Server) startFedimintGuardian(ctx context.Context) error {
	if err := ensureDocker(ctx); err != nil {
		return err
	}
	if err := stopLegacyFedimintIfPresent(ctx); err != nil {
		return err
	}
	paths := fedimintGuardianAppPaths()
	if err := migrateLegacyFedimintGuardian(ctx, paths); err != nil {
		return err
	}
	if err := ensureFedimintGuardianPaths(paths); err != nil {
		return err
	}
	if err := ensureDockerImage(ctx, fedimintdImage); err != nil {
		return err
	}
	values, err := s.resolveFedimintBitcoinBackend(ctx, "Fedimint Guardian")
	if err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, fedimintGuardianComposeContents(paths, values)); err != nil {
		return err
	}
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
		return err
	}
	if err := ensureFedimintBitcoinBackendUfwAccess(ctx, values, fedimintGuardianBridgeName); err != nil && s.logger != nil {
		s.logger.Printf("fedimint guardian: bitcoin rpc ufw rule failed: %v", err)
	}
	if err := ensureFedimintGuardianUfwAccess(ctx); err != nil && s.logger != nil {
		s.logger.Printf("fedimint guardian: ufw rule failed: %v", err)
	}
	return nil
}

func (s *Server) uninstallFedimintGuardian(ctx context.Context) error {
	paths := fedimintGuardianAppPaths()
	if fileExists(paths.ComposePath) {
		_ = runCompose(ctx, paths.Root, paths.ComposePath, "down", "--remove-orphans", "--volumes")
	}
	if err := os.RemoveAll(paths.Root); err != nil {
		return fmt.Errorf("failed to remove guardian app files: %w", err)
	}
	if err := os.RemoveAll(paths.DataRoot); err != nil {
		return fmt.Errorf("failed to remove guardian data files: %w", err)
	}
	if err := removeDockerImage(ctx, fedimintdImage); err != nil {
		return err
	}
	return nil
}

func (s *Server) stopFedimintGuardian(ctx context.Context) error {
	paths := fedimintGuardianAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("Fedimint Guardian is not installed")
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "stop")
}

func (s *Server) installFedimintGateway(ctx context.Context) error {
	return s.startFedimintGateway(ctx)
}

func (s *Server) startFedimintGateway(ctx context.Context) error {
	if err := ensureDocker(ctx); err != nil {
		return err
	}
	if err := stopLegacyFedimintIfPresent(ctx); err != nil {
		return err
	}
	paths := fedimintGatewayAppPaths()
	if err := migrateLegacyFedimintGateway(ctx, paths); err != nil {
		return err
	}
	if err := ensureFedimintGatewayPaths(paths); err != nil {
		return err
	}
	if err := ensureFedimintLndFiles(); err != nil {
		return err
	}
	if err := ensureDockerImage(ctx, fedimintGatewayImage); err != nil {
		return err
	}
	_, passwordHash, err := ensureFedimintGatewayCredentials(paths)
	if err != nil {
		return err
	}
	bitcoinBackend, err := s.resolveFedimintBitcoinBackend(ctx, "Fedimint Lightning Gateway")
	if err != nil {
		return err
	}
	values := fedimintGatewayRuntimeValues{
		GatewayPasswordHash: passwordHash,
		LndRPCAddr:          fedimintLndRPCAddr(s.cfg),
		LndTLSCertPath:      fedimintLndTLSCertPath,
		LndMacaroonPath:     lndAdminMacaroonPath,
		BitcoinBackend:      bitcoinBackend,
	}
	if _, err := ensureFileWithChange(paths.ComposePath, fedimintGatewayComposeContents(paths, values)); err != nil {
		return err
	}
	if err := ensureFedimintLndGrpcAccess(ctx); err != nil {
		return err
	}
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "--no-start"); err != nil && s.logger != nil {
		s.logger.Printf("fedimint gateway: compose up --no-start failed before ufw reconcile: %v", err)
	}
	if err := ensureFedimintGatewayUfwAccess(ctx); err != nil && s.logger != nil {
		s.logger.Printf("fedimint gateway: ufw rule failed: %v", err)
	}
	if err := ensureFedimintBitcoinBackendUfwAccess(ctx, values.BitcoinBackend, fedimintGatewayBridgeName); err != nil && s.logger != nil {
		s.logger.Printf("fedimint gateway: bitcoin rpc ufw rule failed: %v", err)
	}
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
		return err
	}
	if err := waitForComposeServiceStable(ctx, paths.Root, paths.ComposePath, "gatewayd"); err != nil {
		_ = runCompose(ctx, paths.Root, paths.ComposePath, "stop", "gatewayd")
		return fedimintGatewayStartupError(err)
	}
	if err := ensureFedimintGatewayUfwAccess(ctx); err != nil && s.logger != nil {
		s.logger.Printf("fedimint gateway: post-start ufw rule failed: %v", err)
	}
	if err := ensureFedimintBitcoinBackendUfwAccess(ctx, values.BitcoinBackend, fedimintGatewayBridgeName); err != nil && s.logger != nil {
		s.logger.Printf("fedimint gateway: post-start bitcoin rpc ufw rule failed: %v", err)
	}
	return nil
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

func (s *Server) uninstallFedimintGateway(ctx context.Context) error {
	paths := fedimintGatewayAppPaths()
	if fileExists(paths.ComposePath) {
		_ = runCompose(ctx, paths.Root, paths.ComposePath, "down", "--remove-orphans", "--volumes")
	}
	if err := os.RemoveAll(paths.Root); err != nil {
		return fmt.Errorf("failed to remove gateway app files: %w", err)
	}
	if err := os.RemoveAll(paths.DataRoot); err != nil {
		return fmt.Errorf("failed to remove gateway data files: %w", err)
	}
	if err := removeDockerImage(ctx, fedimintGatewayImage); err != nil {
		return err
	}
	return nil
}

func (s *Server) stopFedimintGateway(ctx context.Context) error {
	paths := fedimintGatewayAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("Fedimint Lightning Gateway is not installed")
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "stop")
}

func ensureFedimintGuardianPaths(paths fedimintGuardianPaths) error {
	for _, dir := range []string{paths.Root, paths.DataDir} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("failed to create %s: %w", dir, err)
		}
	}
	return nil
}

func ensureFedimintGatewayPaths(paths fedimintGatewayPaths) error {
	for _, dir := range []string{paths.Root, paths.DataDir, paths.DataRoot} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("failed to create %s: %w", dir, err)
		}
	}
	return nil
}

func ensureFedimintLndFiles() error {
	if !fileExists(fedimintLndTLSCertPath) {
		return fmt.Errorf("LND TLS cert not found at %s", fedimintLndTLSCertPath)
	}
	if !fileExists(lndAdminMacaroonPath) {
		return fmt.Errorf("LND admin macaroon not found at %s", lndAdminMacaroonPath)
	}
	return nil
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

func fedimintLndRPCAddr(cfg *config.Config) string {
	raw := "127.0.0.1:10009"
	if cfg != nil && strings.TrimSpace(cfg.LND.GRPCHost) != "" {
		raw = strings.TrimSpace(cfg.LND.GRPCHost)
	}
	_, port := normalizeBitcoinRPCHostPort(raw, 10009)
	return "https://" + net.JoinHostPort("host.docker.internal", strconv.Itoa(port))
}

func (s *Server) resolveFedimintBitcoinBackend(ctx context.Context, appName string) (fedimintBitcoinBackendValues, error) {
	cfg, ok := readBitcoindRPCConfigFromLNDConf()
	if !ok {
		return fedimintBitcoinBackendValues{}, fmt.Errorf("bitcoin RPC credentials unavailable: configure bitcoind.rpchost, bitcoind.rpcuser and bitcoind.rpcpass in %s before starting %s", lndConfPath, appName)
	}
	bitcoinPaths := bitcoinCoreAppPaths()
	if isLocalRPCHost(cfg.Host) && fileExists(bitcoinPaths.ComposePath) {
		if _, changed, err := syncBitcoinCoreRPCAllowList(ctx, bitcoinPaths); err != nil {
			return fedimintBitcoinBackendValues{}, fmt.Errorf("failed to update local bitcoind RPC allowlist: %w", err)
		} else if changed {
			if err := runCompose(ctx, bitcoinPaths.Root, bitcoinPaths.ComposePath, "restart", "bitcoind"); err != nil {
				return fedimintBitcoinBackendValues{}, fmt.Errorf("failed to restart local bitcoind after RPC allowlist update: %w", err)
			}
		}
	}
	return fedimintBitcoinBackendFromConfig(cfg), nil
}

func fedimintBitcoinBackendFromConfig(cfg bitcoinRPCConfig) fedimintBitcoinBackendValues {
	host, port := parseMainchainRPC(cfg.Host)
	values := fedimintBitcoinBackendValues{
		URL:  "http://" + net.JoinHostPort(host, strconv.Itoa(port)),
		User: cfg.User,
		Pass: cfg.Pass,
	}
	if isLocalRPCHost(cfg.Host) {
		if fileExists(bitcoinCoreAppPaths().ComposePath) {
			values.URL = "http://" + net.JoinHostPort("bitcoind", strconv.Itoa(port))
			values.UseBitcoinCoreNetwork = true
		} else {
			values.URL = "http://" + net.JoinHostPort("host.docker.internal", strconv.Itoa(port))
			values.NeedsLocalRPCBridgeUFW = true
			values.LocalExternalBitcoinPort = port
		}
	}
	return values
}

func fedimintBitcoinBackendNetworkBlocks(values fedimintBitcoinBackendValues) (string, string) {
	if !values.UseBitcoinCoreNetwork {
		return "", ""
	}
	return `    networks:
      - default
      - bitcoincore
`, `
networks:
  default:
  bitcoincore:
    external: true
    name: bitcoincore_default
`
}

func fedimintGuardianComposeContents(paths fedimintGuardianPaths, values fedimintBitcoinBackendValues) string {
	extraNetworks, networkDecl := fedimintBitcoinBackendNetworkBlocks(values)
	return fmt.Sprintf(`services:
  fedimintd:
    image: %s
    restart: unless-stopped
    extra_hosts:
      - "host.docker.internal:host-gateway"
    ports:
      - "%d:%d/tcp"
      - "%d:%d/udp"
      - "%d:%d/udp"
      - "%d:%d/tcp"
    volumes:
      - %s:/data
%s    environment:
      FM_ENABLE_IROH: "true"
      FM_BITCOIN_NETWORK: bitcoin
      FM_BITCOIND_URL: %s
      FM_BITCOIND_USERNAME: %s
      FM_BITCOIND_PASSWORD: %s
      FM_BIND_P2P: 0.0.0.0:%d
      FM_BIND_API: 0.0.0.0:%d
      FM_BIND_UI: 0.0.0.0:%d
%s`, fedimintdImage, fedimintP2PPort, fedimintP2PPort, fedimintP2PPort, fedimintP2PPort, fedimintAPIPort, fedimintAPIPort, fedimintUIPort, fedimintUIPort, paths.DataDir, extraNetworks, yamlSingleQuote(values.URL), yamlSingleQuote(values.User), yamlSingleQuote(values.Pass), fedimintP2PPort, fedimintAPIPort, fedimintUIPort, networkDecl)
}

func yamlSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func isFedimintLogService(service string) bool {
	switch strings.ToLower(strings.TrimSpace(service)) {
	case fedimintGuardianAppID, "fedimintd":
		return true
	case fedimintGatewayAppID, "gatewayd", "fedimint-lightning-gateway":
		return true
	default:
		return false
	}
}

func readFedimintComposeLogLines(ctx context.Context, service string, lines int, since string) ([]string, string, error) {
	switch strings.ToLower(strings.TrimSpace(service)) {
	case fedimintGuardianAppID, "fedimintd":
		paths := fedimintGuardianAppPaths()
		if !fileExists(paths.ComposePath) {
			return nil, "", errFedimintLogServiceNotInstalled
		}
		out, err := readComposeServiceLogLines(ctx, paths.Root, paths.ComposePath, "fedimintd", lines, since)
		return out, "docker:fedimintd", err
	case fedimintGatewayAppID, "gatewayd", "fedimint-lightning-gateway":
		paths := fedimintGatewayAppPaths()
		if !fileExists(paths.ComposePath) {
			return nil, "", errFedimintLogServiceNotInstalled
		}
		out, err := readComposeServiceLogLines(ctx, paths.Root, paths.ComposePath, "gatewayd", lines, since)
		return out, "docker:gatewayd", err
	default:
		return nil, "", errFedimintLogServiceNotInstalled
	}
}

func fedimintGatewayComposeContents(paths fedimintGatewayPaths, values fedimintGatewayRuntimeValues) string {
	extraNetworks, networkDecl := fedimintBitcoinBackendNetworkBlocks(values.BitcoinBackend)
	return fmt.Sprintf(`services:
  gatewayd:
    image: %s
    command: gatewayd lnd
    restart: unless-stopped
    extra_hosts:
      - "host.docker.internal:host-gateway"
    ports:
      - "%d:%d/tcp"
      - "%d:%d/udp"
    volumes:
      - %s:/data
      - /data/lnd:/data/lnd:ro
%s    environment:
      FM_GATEWAY_DATA_DIR: /data
      FM_GATEWAY_LISTEN_ADDR: 0.0.0.0:%d
      FM_GATEWAY_NETWORK: bitcoin
      FM_GATEWAY_IROH_LISTEN_ADDR: 0.0.0.0:%d
      FM_GATEWAY_BCRYPT_PASSWORD_HASH: "%s"
      FM_BITCOIND_URL: %s
      FM_BITCOIND_USERNAME: %s
      FM_BITCOIND_PASSWORD: %s
      FM_LND_RPC_ADDR: %s
      FM_LND_TLS_CERT: %s
      FM_LND_MACAROON: %s
%s`, fedimintGatewayImage, fedimintGatewayUIPort, fedimintGatewayUIPort, fedimintGatewayIrohPort, fedimintGatewayIrohPort, paths.DataDir, extraNetworks, fedimintGatewayUIPort, fedimintGatewayIrohPort, escapeComposeDollar(values.GatewayPasswordHash), yamlSingleQuote(values.BitcoinBackend.URL), yamlSingleQuote(values.BitcoinBackend.User), yamlSingleQuote(values.BitcoinBackend.Pass), values.LndRPCAddr, values.LndTLSCertPath, values.LndMacaroonPath, networkDecl)
}

func escapeComposeDollar(value string) string {
	return strings.ReplaceAll(value, "$", "$$")
}

func ensureFedimintLndGrpcAccess(ctx context.Context) error {
	gateways := []string{}
	if bridgeIP, err := dockerGatewayIP(ctx); err == nil && bridgeIP != "" {
		gateways = append(gateways, bridgeIP)
	}
	if len(gateways) == 0 {
		return errors.New("unable to determine docker gateway IPs")
	}

	content, err := os.ReadFile(lndConfPath)
	if err != nil {
		return fmt.Errorf("failed to read lnd.conf: %w", err)
	}
	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	lines, changed := addLndGrpcAccessOptions(lines, gateways)
	if !changed {
		return nil
	}
	if err := os.WriteFile(lndConfPath, []byte(strings.Join(lines, "\n")+"\n"), 0640); err != nil {
		return fmt.Errorf("failed to update lnd.conf: %w", err)
	}
	_, _ = system.RunCommandWithSudo(ctx, "rm", "-f", "/data/lnd/tls.cert", "/data/lnd/tls.key")
	if _, err := system.RunCommandWithSudo(ctx, "systemctl", "restart", "lnd"); err != nil {
		return fmt.Errorf("failed to restart lnd: %w", err)
	}
	return nil
}

func addLndGrpcAccessOptions(lines []string, gateways []string) ([]string, bool) {
	cleanedGateways := []string{}
	for _, gateway := range gateways {
		trimmed := strings.TrimSpace(gateway)
		if trimmed == "" || stringInSlice(trimmed, cleanedGateways) {
			continue
		}
		cleanedGateways = append(cleanedGateways, trimmed)
	}
	allowedGateways := map[string]bool{}
	for _, gateway := range cleanedGateways {
		allowedGateways[gateway] = true
	}

	removedStaleDockerLine := false
	updated := []string{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isStaleDockerGrpcLine(trimmed, allowedGateways) {
			removedStaleDockerLine = true
			continue
		}
		updated = append(updated, line)
	}

	insertIdx := -1
	for i, line := range updated {
		if strings.EqualFold(strings.TrimSpace(line), "[Application Options]") {
			insertIdx = i + 1
			break
		}
	}
	if insertIdx == -1 {
		updated = append(updated, "[Application Options]")
		insertIdx = len(updated)
	}

	exists := map[string]bool{}
	for _, line := range updated {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		exists[trimmed] = true
	}

	block := []string{}
	if !exists["tlsextradomain=host.docker.internal"] {
		block = append(block, "tlsextradomain=host.docker.internal")
	}
	if !exists["rpclisten=127.0.0.1:10009"] {
		block = append(block, "rpclisten=127.0.0.1:10009")
	}
	for _, gateway := range cleanedGateways {
		tlsLine := "tlsextraip=" + gateway
		if !exists[tlsLine] {
			block = append(block, tlsLine)
		}
		rpcLine := "rpclisten=" + gateway + ":10009"
		if !exists[rpcLine] {
			block = append(block, rpcLine)
		}
	}
	if len(block) == 0 {
		if removedStaleDockerLine {
			return updated, true
		}
		return lines, false
	}

	next := append([]string{}, updated[:insertIdx]...)
	next = append(next, block...)
	next = append(next, updated[insertIdx:]...)
	return next, true
}

func isStaleDockerGrpcLine(trimmed string, allowedGateways map[string]bool) bool {
	switch {
	case strings.HasPrefix(trimmed, "tlsextraip="):
		ip := strings.TrimSpace(strings.TrimPrefix(trimmed, "tlsextraip="))
		return isLikelyDockerGatewayIP(ip) && !allowedGateways[ip]
	case strings.HasPrefix(trimmed, "rpclisten="):
		addr := strings.TrimSpace(strings.TrimPrefix(trimmed, "rpclisten="))
		host, port, err := net.SplitHostPort(addr)
		if err != nil || port != "10009" {
			return false
		}
		return isLikelyDockerGatewayIP(host) && !allowedGateways[host]
	default:
		return false
	}
}

func ensureFedimintGuardianUfwAccess(ctx context.Context) error {
	statusOut, err := system.RunCommandWithSudo(ctx, "ufw", "status")
	if err != nil || !strings.Contains(strings.ToLower(statusOut), "status: active") {
		return nil
	}

	var lastErr error
	for _, rule := range [][3]string{
		{strconv.Itoa(fedimintP2PPort), "tcp", "guardian p2p tcp"},
		{strconv.Itoa(fedimintP2PPort), "udp", "guardian p2p iroh"},
		{strconv.Itoa(fedimintAPIPort), "udp", "guardian api iroh"},
		{strconv.Itoa(fedimintUIPort), "tcp", "guardian ui"},
	} {
		if _, err := system.RunCommandWithSudo(ctx, "ufw", "allow", rule[0]+"/"+rule[1]); err != nil {
			lastErr = fmt.Errorf("%s: %w", rule[2], err)
		}
	}
	return lastErr
}

func ensureFedimintGatewayUfwAccess(ctx context.Context) error {
	statusOut, err := system.RunCommandWithSudo(ctx, "ufw", "status")
	if err != nil || !strings.Contains(strings.ToLower(statusOut), "status: active") {
		return nil
	}

	var lastErr error
	for _, rule := range [][3]string{
		{strconv.Itoa(fedimintGatewayUIPort), "tcp", "gateway ui"},
		{strconv.Itoa(fedimintGatewayIrohPort), "udp", "gateway iroh"},
	} {
		if _, err := system.RunCommandWithSudo(ctx, "ufw", "allow", rule[0]+"/"+rule[1]); err != nil {
			lastErr = fmt.Errorf("%s: %w", rule[2], err)
		}
	}

	if err := allowFedimintBridgePort(ctx, fedimintDockerBridgeName, 10009); err != nil {
		lastErr = err
	}
	if bridge, bridgeErr := fedimintGatewayBridgeName(ctx); bridgeErr == nil && bridge != "" {
		if err := allowFedimintBridgePort(ctx, bridge, 10009); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func ensureFedimintBitcoinBackendUfwAccess(ctx context.Context, values fedimintBitcoinBackendValues, bridgeName func(context.Context) (string, error)) error {
	if !values.NeedsLocalRPCBridgeUFW || values.LocalExternalBitcoinPort <= 0 {
		return nil
	}
	statusOut, err := system.RunCommandWithSudo(ctx, "ufw", "status")
	if err != nil || !strings.Contains(strings.ToLower(statusOut), "status: active") {
		return nil
	}

	var lastErr error
	if err := allowFedimintBridgePort(ctx, fedimintDockerBridgeName, values.LocalExternalBitcoinPort); err != nil {
		lastErr = err
	}
	if bridge, bridgeErr := bridgeName(ctx); bridgeErr == nil && bridge != "" {
		if err := allowFedimintBridgePort(ctx, bridge, values.LocalExternalBitcoinPort); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func allowFedimintBridgePort(ctx context.Context, bridge string, port int) error {
	var lastErr error
	for attempt := 0; attempt < fedimintUfwRetries; attempt++ {
		if _, err := system.RunCommandWithSudo(ctx, "ufw", "allow", "in", "on", bridge, "to", "any", "port", strconv.Itoa(port), "proto", "tcp"); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("failed to apply ufw rule for %s:%d: %w", bridge, port, lastErr)
}

func fedimintGatewayBridgeName(ctx context.Context) (string, error) {
	return dockerComposeBridgeName(ctx, fedimintGatewayNetwork)
}

func fedimintGuardianBridgeName(ctx context.Context) (string, error) {
	return dockerComposeBridgeName(ctx, fedimintGuardianNetwork)
}

func dockerComposeBridgeName(ctx context.Context, networkName string) (string, error) {
	out, err := system.RunCommandWithSudo(ctx, "docker", "network", "inspect", networkName, "--format", "{{.Id}}")
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(out)
	if id == "" || id == "<no value>" {
		return "", fmt.Errorf("%s network id not found", networkName)
	}
	if len(id) > 12 {
		id = id[:12]
	}
	return "br-" + id, nil
}

func migrateLegacyFedimintGuardian(ctx context.Context, paths fedimintGuardianPaths) error {
	legacy := fedimintLegacyAppPaths()
	if !dirExists(legacy.FedimintDataDir) || dirExists(paths.DataDir) {
		return nil
	}
	if err := stopLegacyFedimint(ctx, legacy); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.DataRoot, 0750); err != nil {
		return fmt.Errorf("failed to create guardian data root: %w", err)
	}
	if err := os.Rename(legacy.FedimintDataDir, paths.DataDir); err != nil {
		return fmt.Errorf("failed to migrate legacy guardian data: %w", err)
	}
	cleanupLegacyFedimintDirs(legacy)
	return nil
}

func migrateLegacyFedimintGateway(ctx context.Context, paths fedimintGatewayPaths) error {
	legacy := fedimintLegacyAppPaths()
	hasLegacyGatewayData := dirExists(legacy.GatewayDataDir)
	hasLegacyGatewaySecret := fileExists(legacy.GatewayAdminPasswordPath) || fileExists(legacy.GatewayPasswordHashPath)
	if (!hasLegacyGatewayData && !hasLegacyGatewaySecret) || dirExists(paths.DataDir) {
		return nil
	}
	if err := stopLegacyFedimint(ctx, legacy); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.DataRoot, 0750); err != nil {
		return fmt.Errorf("failed to create gateway data root: %w", err)
	}
	if hasLegacyGatewayData {
		if err := os.Rename(legacy.GatewayDataDir, paths.DataDir); err != nil {
			return fmt.Errorf("failed to migrate legacy gateway data: %w", err)
		}
	}
	if fileExists(legacy.GatewayAdminPasswordPath) && !fileExists(paths.AdminPasswordPath) {
		if err := os.Rename(legacy.GatewayAdminPasswordPath, paths.AdminPasswordPath); err != nil {
			return fmt.Errorf("failed to migrate legacy gateway admin password: %w", err)
		}
	}
	if fileExists(legacy.GatewayPasswordHashPath) && !fileExists(paths.PasswordHashPath) {
		if err := os.Rename(legacy.GatewayPasswordHashPath, paths.PasswordHashPath); err != nil {
			return fmt.Errorf("failed to migrate legacy gateway password hash: %w", err)
		}
	}
	cleanupLegacyFedimintDirs(legacy)
	return nil
}

func stopLegacyFedimint(ctx context.Context, legacy fedimintLegacyPaths) error {
	if fileExists(legacy.ComposePath) {
		if err := runCompose(ctx, legacy.Root, legacy.ComposePath, "down", "--remove-orphans"); err != nil {
			return fmt.Errorf("failed to stop legacy Fedimint app: %w", err)
		}
	}
	if err := os.RemoveAll(legacy.Root); err != nil {
		return fmt.Errorf("failed to remove legacy Fedimint app files: %w", err)
	}
	return nil
}

func stopLegacyFedimintIfPresent(ctx context.Context) error {
	legacy := fedimintLegacyAppPaths()
	if !fileExists(legacy.ComposePath) {
		return nil
	}
	return stopLegacyFedimint(ctx, legacy)
}

func cleanupLegacyFedimintDirs(legacy fedimintLegacyPaths) {
	_ = os.Remove(legacy.DataRoot)
}

func removeDockerImage(ctx context.Context, image string) error {
	out, err := system.RunCommandWithSudo(ctx, "docker", "image", "rm", "-f", image)
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(out)
	if msg == "" {
		msg = err.Error()
	}
	if strings.Contains(strings.ToLower(msg), "no such image") {
		return nil
	}
	return fmt.Errorf("failed to remove docker image %s: %s", image, msg)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
