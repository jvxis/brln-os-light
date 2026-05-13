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
	fedimintAppID           = "fedimint"
	fedimintdImage          = "fedimint/fedimintd:v0.11.1"
	fedimintGatewayImage    = "fedimint/gatewayd:v0.11.1"
	fedimintP2PPort         = 8173
	fedimintAPIPort         = 8174
	fedimintUIPort          = 8175
	fedimintGatewayUIPort   = 8176
	fedimintGatewayIrohPort = 8177
	fedimintLndTLSCertPath  = "/data/lnd/tls.cert"
	fedimintNetworkName     = "fedimint_default"
	fedimintUfwRetries      = 5
)

type fedimintPaths struct {
	Root                     string
	FedimintDataDir          string
	GatewayDataDir           string
	ComposePath              string
	EnvPath                  string
	GatewayAdminPasswordPath string
	GatewayPasswordHashPath  string
}

type fedimintRuntimeValues struct {
	BitcoinRPCURL            string
	BitcoinRPCUser           string
	BitcoinRPCPass           string
	UseBitcoinCoreNetwork    bool
	NeedsLocalRPCBridgeUFW   bool
	LocalExternalBitcoinPort int
	GatewayPasswordHash      string
	LndRPCAddr               string
	LndTLSCertPath           string
	LndMacaroonPath          string
}

type fedimintApp struct {
	server *Server
}

func newFedimintApp(s *Server) appHandler {
	return fedimintApp{server: s}
}

func fedimintDefinition() appDefinition {
	return appDefinition{
		ID:          fedimintAppID,
		Name:        "Fedimint",
		Description: "Run a Fedimint guardian and Lightning gateway using your existing Bitcoin and LND.",
		Port:        fedimintUIPort,
	}
}

func (a fedimintApp) Definition() appDefinition {
	return fedimintDefinition()
}

func (a fedimintApp) Info(ctx context.Context) (appInfo, error) {
	def := a.Definition()
	info := newAppInfo(def)
	paths := fedimintAppPaths()
	if !fileExists(paths.ComposePath) {
		return info, nil
	}
	info.Installed = true
	info.AdminPasswordPath = paths.GatewayAdminPasswordPath

	fedimintdStatus, fedimintdErr := getComposeStatus(ctx, paths.Root, paths.ComposePath, "fedimintd")
	gatewayStatus, gatewayErr := getComposeStatus(ctx, paths.Root, paths.ComposePath, "gatewayd")
	if fedimintdErr != nil {
		info.Status = "unknown"
		return info, fedimintdErr
	}
	if gatewayErr != nil {
		info.Status = "unknown"
		return info, gatewayErr
	}
	if fedimintdStatus == "running" && gatewayStatus == "running" {
		info.Status = "running"
		return info, nil
	}
	info.Status = "stopped"
	return info, nil
}

func (a fedimintApp) Install(ctx context.Context) error {
	return a.server.applyFedimint(ctx)
}

func (a fedimintApp) Uninstall(ctx context.Context) error {
	return a.server.uninstallFedimint(ctx)
}

func (a fedimintApp) Start(ctx context.Context) error {
	return a.server.applyFedimint(ctx)
}

func (a fedimintApp) Stop(ctx context.Context) error {
	return a.server.stopFedimint(ctx)
}

func fedimintAppPaths() fedimintPaths {
	root := filepath.Join(appsRoot, fedimintAppID)
	dataRoot := filepath.Join(appsDataRoot, fedimintAppID)
	return fedimintPaths{
		Root:                     root,
		FedimintDataDir:          filepath.Join(dataRoot, "fedimintd"),
		GatewayDataDir:           filepath.Join(dataRoot, "gatewayd"),
		ComposePath:              filepath.Join(root, "docker-compose.yaml"),
		EnvPath:                  filepath.Join(root, ".env"),
		GatewayAdminPasswordPath: filepath.Join(dataRoot, "gateway-admin.txt"),
		GatewayPasswordHashPath:  filepath.Join(dataRoot, "gateway-password-hash.txt"),
	}
}

func (s *Server) applyFedimint(ctx context.Context) error {
	if err := ensureDocker(ctx); err != nil {
		return err
	}
	paths := fedimintAppPaths()
	if err := ensureFedimintPaths(paths); err != nil {
		return err
	}
	if err := ensureFedimintLndFiles(); err != nil {
		return err
	}
	if err := ensureFedimintImages(ctx); err != nil {
		return err
	}

	values, err := s.resolveFedimintRuntimeValues(ctx)
	if err != nil {
		return err
	}
	if _, values.GatewayPasswordHash, err = ensureFedimintGatewayCredentials(paths); err != nil {
		return err
	}

	if err := ensureFedimintEnv(paths, values); err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, fedimintComposeContents(paths, values)); err != nil {
		return err
	}

	// Start fedimintd first so Docker creates fedimint_default before we add
	// the LND listener for this app's bridge. gatewayd starts after LND access
	// is reconciled.
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d", "fedimintd"); err != nil {
		return err
	}
	if err := ensureFedimintLndGrpcAccess(ctx); err != nil {
		return err
	}
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
		return err
	}
	if err := ensureFedimintUfwAccess(ctx, values); err != nil && s.logger != nil {
		s.logger.Printf("fedimint: ufw rule failed: %v", err)
	}
	return nil
}

func (s *Server) uninstallFedimint(ctx context.Context) error {
	paths := fedimintAppPaths()
	if fileExists(paths.ComposePath) {
		_ = runCompose(ctx, paths.Root, paths.ComposePath, "down", "--remove-orphans")
	}
	if err := os.RemoveAll(paths.Root); err != nil {
		return fmt.Errorf("failed to remove app files: %w", err)
	}
	return nil
}

func (s *Server) stopFedimint(ctx context.Context) error {
	paths := fedimintAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("Fedimint is not installed")
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "stop")
}

func ensureFedimintPaths(paths fedimintPaths) error {
	for _, dir := range []string{paths.Root, paths.FedimintDataDir, paths.GatewayDataDir, filepath.Dir(paths.GatewayAdminPasswordPath)} {
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

func ensureFedimintImages(ctx context.Context) error {
	if err := ensureDockerImage(ctx, fedimintdImage); err != nil {
		return err
	}
	if err := ensureDockerImage(ctx, fedimintGatewayImage); err != nil {
		return err
	}
	return nil
}

func ensureFedimintGatewayCredentials(paths fedimintPaths) (string, string, error) {
	password := readSecretFile(paths.GatewayAdminPasswordPath)
	if password == "" {
		var err error
		password, err = randomToken(24)
		if err != nil {
			return "", "", fmt.Errorf("failed to generate gateway password: %w", err)
		}
		if err := writeFile(paths.GatewayAdminPasswordPath, password+"\n", 0600); err != nil {
			return "", "", err
		}
	}

	hash := readSecretFile(paths.GatewayPasswordHashPath)
	if hash == "" {
		rawHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return "", "", fmt.Errorf("failed to hash gateway password: %w", err)
		}
		hash = string(rawHash)
		if err := writeFile(paths.GatewayPasswordHashPath, hash+"\n", 0600); err != nil {
			return "", "", err
		}
	}
	return password, hash, nil
}

func (s *Server) resolveFedimintRuntimeValues(ctx context.Context) (fedimintRuntimeValues, error) {
	values := fedimintRuntimeValues{
		LndRPCAddr:      fedimintLndRPCAddr(s.cfg),
		LndTLSCertPath:  fedimintLndTLSCertPath,
		LndMacaroonPath: lndAdminMacaroonPath,
	}

	switch readBitcoinSource() {
	case "local":
		localCfg, updated, err := readBitcoinLocalRPCConfig(ctx)
		if err != nil {
			return fedimintRuntimeValues{}, fmt.Errorf("local bitcoin RPC unavailable: %w", err)
		}
		if strings.TrimSpace(localCfg.User) == "" || strings.TrimSpace(localCfg.Pass) == "" {
			return fedimintRuntimeValues{}, errors.New("local bitcoin RPC credentials missing")
		}
		_, port := parseMainchainRPC(localCfg.Host)

		if fileExists(bitcoinCoreAppPaths().ComposePath) {
			if updated {
				bitcoinPaths := bitcoinCoreAppPaths()
				if restartErr := runCompose(ctx, bitcoinPaths.Root, bitcoinPaths.ComposePath, "restart", "bitcoind"); restartErr != nil {
					return fedimintRuntimeValues{}, fmt.Errorf("failed to restart local bitcoind after RPC allowlist update: %w", restartErr)
				}
			}
			values.BitcoinRPCURL = fedimintHTTPRPCURL("bitcoind", port)
			values.BitcoinRPCUser = localCfg.User
			values.BitcoinRPCPass = localCfg.Pass
			values.UseBitcoinCoreNetwork = true
			return values, nil
		}

		values.BitcoinRPCURL = fedimintHTTPRPCURL("host.docker.internal", port)
		values.BitcoinRPCUser = localCfg.User
		values.BitcoinRPCPass = localCfg.Pass
		values.NeedsLocalRPCBridgeUFW = true
		values.LocalExternalBitcoinPort = port
		return values, nil
	case "remote":
		remoteCfg, err := resolveFedimintRemoteBitcoinRPCConfig(s.cfg)
		if err != nil {
			return fedimintRuntimeValues{}, err
		}
		host, port := parseMainchainRPC(remoteCfg.Host)
		values.BitcoinRPCURL = fedimintHTTPRPCURL(host, port)
		values.BitcoinRPCUser = remoteCfg.User
		values.BitcoinRPCPass = remoteCfg.Pass
		return values, nil
	default:
		return fedimintRuntimeValues{}, errors.New("bitcoin source is not configured")
	}
}

func resolveFedimintRemoteBitcoinRPCConfig(cfg *config.Config) (bitcoinRPCConfig, error) {
	if remoteCfg, ok := readBitcoinTaggedRPCConfigFromLNDConf("remote"); ok {
		return remoteCfg, nil
	}
	if remoteCfg, ok := readBitcoindRPCConfigFromLNDConf(); ok && !isLocalRPCHost(remoteCfg.Host) {
		return remoteCfg, nil
	}
	if cfg == nil {
		return bitcoinRPCConfig{}, errors.New("bitcoin remote config unavailable")
	}
	user, pass := readBitcoinSecrets()
	if strings.TrimSpace(user) == "" || strings.TrimSpace(pass) == "" {
		return bitcoinRPCConfig{}, errors.New("bitcoin remote RPC credentials missing")
	}
	return bitcoinRPCConfig{
		Host: cfg.BitcoinRemote.RPCHost,
		User: user,
		Pass: pass,
	}, nil
}

func fedimintLndRPCAddr(cfg *config.Config) string {
	raw := "127.0.0.1:10009"
	if cfg != nil && strings.TrimSpace(cfg.LND.GRPCHost) != "" {
		raw = strings.TrimSpace(cfg.LND.GRPCHost)
	}
	_, port := normalizeBitcoinRPCHostPort(raw, 10009)
	return "https://" + net.JoinHostPort("host.docker.internal", strconv.Itoa(port))
}

func fedimintHTTPRPCURL(host string, port int) string {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" {
		trimmed = "127.0.0.1"
	}
	if port <= 0 {
		port = 8332
	}
	return "http://" + net.JoinHostPort(trimmed, strconv.Itoa(port))
}

func ensureFedimintEnv(paths fedimintPaths, values fedimintRuntimeValues) error {
	required := [][2]string{
		{"FEDIMINT_BITCOIN_RPC_URL", values.BitcoinRPCURL},
		{"FEDIMINT_BITCOIN_RPC_USER", values.BitcoinRPCUser},
		{"FEDIMINT_BITCOIN_RPC_PASS", values.BitcoinRPCPass},
	}
	if !fileExists(paths.EnvPath) {
		lines := make([]string, 0, len(required)+1)
		for _, kv := range required {
			lines = append(lines, kv[0]+"="+kv[1])
		}
		lines = append(lines, "")
		return writeFile(paths.EnvPath, strings.Join(lines, "\n"), 0600)
	}
	for _, kv := range required {
		exists, value, err := envValueState(paths.EnvPath, kv[0])
		if err != nil {
			return err
		}
		if !exists {
			if err := appendEnvLine(paths.EnvPath, kv[0], kv[1]); err != nil {
				return err
			}
			continue
		}
		if strings.TrimSpace(value) != kv[1] {
			if err := setEnvValue(paths.EnvPath, kv[0], kv[1]); err != nil {
				return err
			}
		}
	}
	return nil
}

func fedimintComposeContents(paths fedimintPaths, values fedimintRuntimeValues) string {
	fedimintdNetworks := ""
	gatewayNetworks := ""
	extraNetworkDecl := ""
	if values.UseBitcoinCoreNetwork {
		fedimintdNetworks = "    networks:\n      - default\n      - bitcoincore\n"
		gatewayNetworks = "    networks:\n      - default\n      - bitcoincore\n"
		extraNetworkDecl = "\n  bitcoincore:\n    external: true\n    name: bitcoincore_default\n"
	}

	return fmt.Sprintf(`services:
  fedimintd:
    image: %[1]s
    restart: unless-stopped
    env_file:
      - ./.env
    extra_hosts:
      - "host.docker.internal:host-gateway"
    ports:
      - "%[6]d:%[6]d/tcp"
      - "%[6]d:%[6]d/udp"
      - "%[7]d:%[7]d/udp"
      - "%[8]d:%[8]d/tcp"
    volumes:
      - %[3]s:/data
    environment:
      FM_ENABLE_IROH: "true"
      FM_BITCOIN_NETWORK: bitcoin
      FM_BITCOIND_URL: ${FEDIMINT_BITCOIN_RPC_URL}
      FM_BITCOIND_USERNAME: ${FEDIMINT_BITCOIN_RPC_USER}
      FM_BITCOIND_PASSWORD: ${FEDIMINT_BITCOIN_RPC_PASS}
      FM_BIND_P2P: 0.0.0.0:%[6]d
      FM_BIND_API: 0.0.0.0:%[7]d
      FM_BIND_UI: 0.0.0.0:%[8]d
%[13]s
  gatewayd:
    image: %[2]s
    command: gatewayd lnd
    restart: unless-stopped
    depends_on:
      - fedimintd
    env_file:
      - ./.env
    extra_hosts:
      - "host.docker.internal:host-gateway"
    ports:
      - "%[9]d:%[9]d/tcp"
      - "%[10]d:%[10]d/udp"
    volumes:
      - %[4]s:/data
      - /data/lnd:/data/lnd:ro
    environment:
      FM_GATEWAY_DATA_DIR: /data
      FM_GATEWAY_LISTEN_ADDR: 0.0.0.0:%[9]d
      FM_GATEWAY_NETWORK: bitcoin
      FM_GATEWAY_IROH_LISTEN_ADDR: 0.0.0.0:%[10]d
      FM_GATEWAY_BCRYPT_PASSWORD_HASH: "%[11]s"
      FM_BITCOIND_URL: ${FEDIMINT_BITCOIN_RPC_URL}
      FM_BITCOIND_USERNAME: ${FEDIMINT_BITCOIN_RPC_USER}
      FM_BITCOIND_PASSWORD: ${FEDIMINT_BITCOIN_RPC_PASS}
      FM_LND_RPC_ADDR: %[12]s
      FM_LND_TLS_CERT: %[14]s
      FM_LND_MACAROON: %[15]s
%[16]s
networks:
  default:
%[17]s`, fedimintdImage, fedimintGatewayImage, paths.FedimintDataDir, paths.GatewayDataDir, paths.EnvPath, fedimintP2PPort, fedimintAPIPort, fedimintUIPort, fedimintGatewayUIPort, fedimintGatewayIrohPort, escapeComposeDollar(values.GatewayPasswordHash), values.LndRPCAddr, fedimintdNetworks, values.LndTLSCertPath, values.LndMacaroonPath, gatewayNetworks, extraNetworkDecl)
}

func escapeComposeDollar(value string) string {
	return strings.ReplaceAll(value, "$", "$$")
}

func ensureFedimintLndGrpcAccess(ctx context.Context) error {
	gateways := []string{}
	if bridgeIP, err := dockerGatewayIP(ctx); err == nil && bridgeIP != "" {
		gateways = append(gateways, bridgeIP)
	}
	if gatewayIP, err := fedimintNetworkGatewayIP(ctx); err == nil && gatewayIP != "" && !stringInSlice(gatewayIP, gateways) {
		gateways = append(gateways, gatewayIP)
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

	updated := append([]string{}, lines...)
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
		return lines, false
	}

	next := append([]string{}, updated[:insertIdx]...)
	next = append(next, block...)
	next = append(next, updated[insertIdx:]...)
	return next, true
}

func fedimintNetworkGatewayIP(ctx context.Context) (string, error) {
	out, err := system.RunCommandWithSudo(ctx, "docker", "network", "inspect", fedimintNetworkName, "--format", "{{(index .IPAM.Config 0).Gateway}}")
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(out)
	if ip == "" || ip == "<no value>" {
		return "", errors.New("fedimint_default network gateway not found")
	}
	return ip, nil
}

func ensureFedimintUfwAccess(ctx context.Context, values fedimintRuntimeValues) error {
	statusOut, err := system.RunCommandWithSudo(ctx, "ufw", "status")
	if err != nil || !strings.Contains(strings.ToLower(statusOut), "status: active") {
		return nil
	}

	var lastErr error
	for _, rule := range [][3]string{
		{strconv.Itoa(fedimintP2PPort), "tcp", "guardian p2p tcp"},
		{strconv.Itoa(fedimintP2PPort), "udp", "guardian p2p udp"},
		{strconv.Itoa(fedimintAPIPort), "udp", "guardian api udp"},
		{strconv.Itoa(fedimintUIPort), "tcp", "guardian ui"},
		{strconv.Itoa(fedimintGatewayUIPort), "tcp", "gateway ui"},
		{strconv.Itoa(fedimintGatewayIrohPort), "udp", "gateway iroh"},
	} {
		if _, err := system.RunCommandWithSudo(ctx, "ufw", "allow", rule[0]+"/"+rule[1]); err != nil {
			lastErr = fmt.Errorf("%s: %w", rule[2], err)
		}
	}

	bridge, bridgeErr := fedimintBridgeName(ctx)
	if bridgeErr != nil || bridge == "" {
		if lastErr != nil {
			return lastErr
		}
		return bridgeErr
	}
	if err := allowFedimintBridgePort(ctx, bridge, 10009); err != nil {
		lastErr = err
	}
	if values.NeedsLocalRPCBridgeUFW {
		port := values.LocalExternalBitcoinPort
		if port <= 0 {
			port = 8332
		}
		if err := allowFedimintBridgePort(ctx, bridge, port); err != nil {
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

func fedimintBridgeName(ctx context.Context) (string, error) {
	out, err := system.RunCommandWithSudo(ctx, "docker", "network", "inspect", fedimintNetworkName, "--format", "{{.Id}}")
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(out)
	if id == "" || id == "<no value>" {
		return "", errors.New("fedimint_default network id not found")
	}
	if len(id) > 12 {
		id = id[:12]
	}
	return "br-" + id, nil
}
