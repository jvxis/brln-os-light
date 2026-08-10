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

	"lightningos-light/internal/system"
)

const (
	publicPoolAppID        = "publicpool"
	publicPoolStratumPort  = 3333
	publicPoolAPIPort      = 3334
	publicPoolUIPort       = 8081
	publicPoolDefaultNet   = "mainnet"
	publicPoolDefaultIDTag = "LightningOS-PublicPool"
	publicPoolUfwRetries   = 5
)

var publicPoolBackendImageCandidates = []string{
	"sethforprivacy/public-pool:latest",
	"smolgrrr/public-pool:latest",
}

var publicPoolUIImageCandidates = []string{
	"sethforprivacy/public-pool-ui:latest",
	"smolgrrr/public-pool-ui:latest",
}

type publicPoolPaths struct {
	Root        string
	DataDir     string
	ComposePath string
	EnvPath     string
}

type publicPoolApp struct {
	server *Server
}

type publicPoolRuntimeValues struct {
	BackendImage             string
	UIImage                  string
	BitcoinMode              string
	BitcoinRPCURL            string
	BitcoinRPCPort           int
	BitcoinRPCUser           string
	BitcoinRPCPass           string
	BitcoinZMQHost           string
	UseBitcoinCoreNetwork    bool
	NeedsLocalRPCBridgeUFW   bool
	LocalExternalBitcoinPort int
}

func newPublicPoolApp(s *Server) appHandler {
	return publicPoolApp{server: s}
}

func publicPoolDefinition() appDefinition {
	return appDefinition{
		ID:          publicPoolAppID,
		Name:        "Public Pool",
		Description: "Run your own Public Pool backend + web UI with local or remote Bitcoin RPC.",
		Port:        publicPoolUIPort,
	}
}

func (a publicPoolApp) Definition() appDefinition {
	return publicPoolDefinition()
}

func (a publicPoolApp) Info(ctx context.Context) (appInfo, error) {
	def := a.Definition()
	info := newAppInfo(def)
	a.server.enrichPublicPoolUfwInfo(ctx, &info)
	paths := publicPoolAppPaths()
	if !fileExists(paths.ComposePath) {
		return info, nil
	}
	info.Installed = true
	status, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, "public-pool")
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func (s *Server) enrichPublicPoolUfwInfo(ctx context.Context, info *appInfo) {
	if info == nil || readBitcoinSource() != "local" || fileExists(bitcoinCoreAppPaths().ComposePath) {
		return
	}
	if !ufwActive(ctx) {
		return
	}
	port := 8332
	if cfg, _, err := readBitcoinLocalRPCConfig(ctx); err == nil {
		if _, parsedPort := parseMainchainRPC(cfg.Host); parsedPort > 0 {
			port = parsedPort
		}
	}
	info.UFWActive = true
	info.UFWCommand = publicPoolUfwCommand(ctx, port)
}

func (a publicPoolApp) Install(ctx context.Context) error {
	return a.server.installPublicPool(ctx)
}

func (a publicPoolApp) Uninstall(ctx context.Context) error {
	return a.server.uninstallPublicPool(ctx)
}

func (a publicPoolApp) Start(ctx context.Context) error {
	return a.server.startPublicPool(ctx)
}

func (a publicPoolApp) Stop(ctx context.Context) error {
	return a.server.stopPublicPool(ctx)
}

func publicPoolAppPaths() publicPoolPaths {
	root := filepath.Join(appsRoot, publicPoolAppID)
	dataDir := filepath.Join(appsDataRoot, publicPoolAppID, "db")
	return publicPoolPaths{
		Root:        root,
		DataDir:     dataDir,
		ComposePath: filepath.Join(root, "docker-compose.yaml"),
		EnvPath:     filepath.Join(root, ".env"),
	}
}

func (s *Server) installPublicPool(ctx context.Context) error {
	if err := ensureDocker(ctx); err != nil {
		return err
	}
	paths := publicPoolAppPaths()
	if err := ensurePublicPoolPaths(paths); err != nil {
		return err
	}
	values, err := s.resolvePublicPoolRuntimeValues(ctx)
	if err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, publicPoolComposeContents(paths, values)); err != nil {
		return err
	}
	if err := ensurePublicPoolEnv(paths, values); err != nil {
		return err
	}
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
		return err
	}
	if err := ensurePublicPoolUfwAccess(ctx, values); err != nil && s.logger != nil {
		s.logger.Printf("public-pool: ufw rule failed: %v", err)
	}
	return nil
}

func (s *Server) startPublicPool(ctx context.Context) error {
	paths := publicPoolAppPaths()
	if err := ensurePublicPoolPaths(paths); err != nil {
		return err
	}
	values, err := s.resolvePublicPoolRuntimeValues(ctx)
	if err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, publicPoolComposeContents(paths, values)); err != nil {
		return err
	}
	if err := ensurePublicPoolEnv(paths, values); err != nil {
		return err
	}
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
		return err
	}
	if err := ensurePublicPoolUfwAccess(ctx, values); err != nil && s.logger != nil {
		s.logger.Printf("public-pool: ufw rule failed: %v", err)
	}
	return nil
}

func (s *Server) stopPublicPool(ctx context.Context) error {
	paths := publicPoolAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("Public Pool is not installed")
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "stop")
}

func (s *Server) uninstallPublicPool(ctx context.Context) error {
	paths := publicPoolAppPaths()
	if fileExists(paths.ComposePath) {
		_ = runCompose(ctx, paths.Root, paths.ComposePath, "down", "--remove-orphans")
	}
	if err := os.RemoveAll(paths.Root); err != nil {
		return fmt.Errorf("failed to remove app files: %w", err)
	}
	return nil
}

func ensurePublicPoolPaths(paths publicPoolPaths) error {
	if err := os.MkdirAll(paths.Root, 0750); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}
	if err := os.MkdirAll(paths.DataDir, 0750); err != nil {
		return fmt.Errorf("failed to create app data directory: %w", err)
	}
	return nil
}

func (s *Server) resolvePublicPoolRuntimeValues(ctx context.Context) (publicPoolRuntimeValues, error) {
	backendImage, err := ensureFirstAvailableDockerImage(ctx, publicPoolBackendImageCandidates)
	if err != nil {
		return publicPoolRuntimeValues{}, err
	}
	uiImage, err := ensureFirstAvailableDockerImage(ctx, publicPoolUIImageCandidates)
	if err != nil {
		return publicPoolRuntimeValues{}, err
	}

	values := publicPoolRuntimeValues{
		BackendImage: backendImage,
		UIImage:      uiImage,
	}

	source := readBitcoinSource()
	if source == "remote" {
		host, port := parseMainchainRPC(s.cfg.BitcoinRemote.RPCHost)
		user, pass := readBitcoinSecrets()
		if user == "" || pass == "" {
			return publicPoolRuntimeValues{}, errors.New("bitcoin remote RPC credentials missing")
		}
		values.BitcoinMode = "remote"
		values.BitcoinRPCURL = toHTTPRPCURL(host)
		values.BitcoinRPCPort = port
		values.BitcoinRPCUser = user
		values.BitcoinRPCPass = pass
		values.BitcoinZMQHost = normalizePublicPoolZMQHost(s.cfg.BitcoinRemote.ZMQRawBlock)
		return values, nil
	}

	localCfg, localCfgUpdated, err := readBitcoinLocalRPCConfig(ctx)
	if err != nil {
		return publicPoolRuntimeValues{}, fmt.Errorf("local bitcoin RPC unavailable: %w", err)
	}
	if strings.TrimSpace(localCfg.User) == "" || strings.TrimSpace(localCfg.Pass) == "" {
		return publicPoolRuntimeValues{}, errors.New("local bitcoin RPC credentials missing")
	}
	localHost, localPort := parseMainchainRPC(localCfg.Host)

	if fileExists(bitcoinCoreAppPaths().ComposePath) {
		if localCfgUpdated {
			if restartErr := runBitcoinCoreLifecycle(ctx, "restart"); restartErr != nil {
				return publicPoolRuntimeValues{}, fmt.Errorf("failed to restart local bitcoind after RPC allowlist update: %w", restartErr)
			}
		}
		values.BitcoinMode = "local_app"
		values.BitcoinRPCURL = "http://bitcoind"
		values.BitcoinRPCPort = localPort
		values.BitcoinRPCUser = localCfg.User
		values.BitcoinRPCPass = localCfg.Pass
		values.BitcoinZMQHost = publicPoolDockerZMQHost(localCfg.ZMQBlock, "bitcoind")
		values.UseBitcoinCoreNetwork = true
		return values, nil
	}

	values.BitcoinMode = "local_external"
	values.BitcoinRPCURL = publicPoolDockerRPCURL(localHost)
	values.BitcoinRPCPort = localPort
	values.BitcoinRPCUser = localCfg.User
	values.BitcoinRPCPass = localCfg.Pass
	values.BitcoinZMQHost = publicPoolExternalZMQHost(localCfg.ZMQBlock)
	values.NeedsLocalRPCBridgeUFW = true
	values.LocalExternalBitcoinPort = localPort
	return values, nil
}

func ensureFirstAvailableDockerImage(ctx context.Context, candidates []string) (string, error) {
	lastErr := error(nil)
	for _, image := range candidates {
		if strings.TrimSpace(image) == "" {
			continue
		}
		if err := ensureDockerImage(ctx, image); err == nil {
			return image, nil
		} else {
			lastErr = err
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", errors.New("no docker image candidates provided")
}

func toHTTPRPCURL(host string) string {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" {
		trimmed = "127.0.0.1"
	}
	if strings.Contains(trimmed, ":") && !strings.HasPrefix(trimmed, "[") {
		trimmed = "[" + trimmed + "]"
	}
	return "http://" + trimmed
}

func publicPoolDockerRPCURL(host string) string {
	trimmed := strings.TrimSpace(host)
	lower := strings.ToLower(trimmed)
	if lower == "" || lower == "localhost" {
		return "http://host.docker.internal"
	}
	ip := net.ParseIP(strings.Trim(trimmed, "[]"))
	if ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return "http://host.docker.internal"
	}
	return toHTTPRPCURL(trimmed)
}

func normalizePublicPoolZMQHost(endpoint string) string {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return ""
	}
	if !strings.HasPrefix(strings.ToLower(trimmed), "tcp://") {
		return "tcp://" + trimmed
	}
	return trimmed
}

func publicPoolDockerZMQHost(endpoint string, dockerHost string) string {
	normalized := normalizePublicPoolZMQHost(endpoint)
	if normalized == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(strings.TrimPrefix(normalized, "tcp://"))
	if err != nil || strings.TrimSpace(port) == "" {
		return ""
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		host = strings.TrimSpace(dockerHost)
	} else if strings.EqualFold(host, "localhost") {
		host = strings.TrimSpace(dockerHost)
	}
	if host == "" {
		return ""
	}
	return "tcp://" + net.JoinHostPort(host, port)
}

func publicPoolExternalZMQHost(endpoint string) string {
	normalized := normalizePublicPoolZMQHost(endpoint)
	if normalized == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(strings.TrimPrefix(normalized, "tcp://"))
	if err != nil {
		return ""
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return ""
	}
	return normalized
}

func publicPoolComposeContents(paths publicPoolPaths, values publicPoolRuntimeValues) string {
	backendNetworks := ""
	extraNetworkDecl := ""
	if values.UseBitcoinCoreNetwork {
		backendNetworks = "    networks:\n      - default\n      - bitcoincore\n"
		extraNetworkDecl = "\n  bitcoincore:\n    external: true\n    name: bitcoincore_default\n"
	}

	return fmt.Sprintf(`services:
  public-pool:
    image: %s
    restart: unless-stopped
    env_file:
      - ./.env
    extra_hosts:
      - "host.docker.internal:host-gateway"
    ports:
      - "%d:%d"
      - "127.0.0.1:%d:%d"
    volumes:
      - %s:/public-pool/DB
%s
  public-pool-ui:
    image: %s
    restart: unless-stopped
    depends_on:
      - public-pool
    ports:
      - "%d:80"
    environment:
      LOGLEVEL: INFO
      LOGFORMAT: json
      PUBLIC_POOL_UI_API_URL: ""
    entrypoint:
      - /bin/sh
      - -c
      - |
        find /var/www/html -type f \( -name '*.br' -o -name '*.gz' \) -delete
        find /var/www/html -type f -name '*.js' -exec sed -i "s#https://public-pool.io:40557#${PUBLIC_POOL_UI_API_URL:-}#g" {} +
        find /var/www/html -type f -name '*.js' -exec sed -i "s#http://localhost:3334#${PUBLIC_POOL_UI_API_URL:-}#g" {} +
        find /var/www/html -type f -name '*.js' -exec sed -i "s#public-pool.io:21496##g" {} +
        cat >/etc/Caddyfile <<EOF
        :80 {
          @api path /api*
          reverse_proxy @api public-pool:%d
          root * /var/www/html
          file_server
          log {
            output stdout
            format ${LOGFORMAT:-json}
            level ${LOGLEVEL:-INFO}
          }
        }
        EOF
        exec caddy run --config /etc/Caddyfile

networks:
  default:
%s`, values.BackendImage, publicPoolStratumPort, publicPoolStratumPort, publicPoolAPIPort, publicPoolAPIPort, paths.DataDir, backendNetworks, values.UIImage, publicPoolUIPort, publicPoolAPIPort, extraNetworkDecl)
}

func ensurePublicPoolEnv(paths publicPoolPaths, values publicPoolRuntimeValues) error {
	required := [][2]string{
		{"BITCOIN_RPC_URL", values.BitcoinRPCURL},
		{"BITCOIN_RPC_USER", values.BitcoinRPCUser},
		{"BITCOIN_RPC_PASSWORD", values.BitcoinRPCPass},
		{"BITCOIN_RPC_PORT", strconv.Itoa(values.BitcoinRPCPort)},
		{"BITCOIN_RPC_TIMEOUT", "10000"},
		{"BITCOIN_ZMQ_HOST", values.BitcoinZMQHost},
		{"API_PORT", strconv.Itoa(publicPoolAPIPort)},
		{"STRATUM_PORT", strconv.Itoa(publicPoolStratumPort)},
		{"NETWORK", publicPoolDefaultNet},
		{"API_SECURE", "false"},
		{"POOL_IDENTIFIER", publicPoolDefaultIDTag},
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

func ensurePublicPoolUfwAccess(ctx context.Context, values publicPoolRuntimeValues) error {
	if !ufwActive(ctx) {
		return nil
	}

	var lastErr error
	if _, err := system.RunCommandWithSudo(ctx, "ufw", "allow", fmt.Sprintf("%d/tcp", publicPoolUIPort)); err != nil {
		lastErr = err
	}
	if _, err := system.RunCommandWithSudo(ctx, "ufw", "allow", fmt.Sprintf("%d/tcp", publicPoolStratumPort)); err != nil {
		lastErr = err
	}

	if values.NeedsLocalRPCBridgeUFW {
		port := values.LocalExternalBitcoinPort
		if port <= 0 {
			port = 8332
		}
		var bridge string
		var err error
		for attempt := 0; attempt < publicPoolUfwRetries; attempt++ {
			bridge, err = publicPoolBridgeName(ctx)
			if err == nil && bridge != "" {
				if _, allowErr := system.RunCommandWithSudo(ctx, "ufw", "allow", "in", "on", bridge, "to", "any", "port", strconv.Itoa(port), "proto", "tcp"); allowErr == nil {
					return lastErr
				} else {
					err = allowErr
				}
			}
			time.Sleep(2 * time.Second)
		}
		if err != nil {
			if bridge != "" {
				lastErr = fmt.Errorf("failed to apply ufw rule for %s:%d: %w", bridge, port, err)
			} else {
				lastErr = fmt.Errorf("failed to apply ufw rule for public-pool bridge: %w", err)
			}
		}
	}
	return lastErr
}

func ufwActive(ctx context.Context) bool {
	statusOut, err := system.RunCommandWithSudo(ctx, "ufw", "status")
	return err == nil && strings.Contains(strings.ToLower(statusOut), "status: active")
}

func (s *Server) startPublicPoolRuntimeReconciler() {
	go func() {
		time.Sleep(15 * time.Second)
		for attempt := 0; attempt < publicPoolUfwRetries; attempt++ {
			if attempt > 0 {
				time.Sleep(10 * time.Second)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			err := s.reconcileRunningPublicPoolUfwAccess(ctx)
			cancel()
			if err == nil {
				return
			}
			if s.logger != nil {
				s.logger.Printf("public-pool: startup ufw reconcile failed (attempt %d/%d): %v", attempt+1, publicPoolUfwRetries, err)
			}
		}
	}()
}

func (s *Server) reconcileRunningPublicPoolUfwAccess(ctx context.Context) error {
	paths := publicPoolAppPaths()
	if !fileExists(paths.ComposePath) {
		return nil
	}
	status, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, "public-pool")
	if err != nil || status != "running" {
		return err
	}
	values, err := s.resolvePublicPoolUfwRuntimeValues(ctx)
	if err != nil {
		return err
	}
	return ensurePublicPoolUfwAccess(ctx, values)
}

func (s *Server) resolvePublicPoolUfwRuntimeValues(ctx context.Context) (publicPoolRuntimeValues, error) {
	values := publicPoolRuntimeValues{}
	if readBitcoinSource() == "remote" {
		return values, nil
	}
	if fileExists(bitcoinCoreAppPaths().ComposePath) {
		return values, nil
	}
	localCfg, _, err := readBitcoinLocalRPCConfig(ctx)
	if err != nil {
		return publicPoolRuntimeValues{}, fmt.Errorf("local bitcoin RPC unavailable: %w", err)
	}
	_, localPort := parseMainchainRPC(localCfg.Host)
	values.NeedsLocalRPCBridgeUFW = true
	values.LocalExternalBitcoinPort = localPort
	return values, nil
}

func publicPoolBridgeName(ctx context.Context) (string, error) {
	out, err := system.RunCommandWithSudo(ctx, "docker", "network", "inspect", "publicpool_default", "--format", "{{.Id}}")
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(out)
	if id == "" || id == "<no value>" {
		return "", errors.New("publicpool_default network id not found")
	}
	if len(id) > 12 {
		id = id[:12]
	}
	return "br-" + id, nil
}

func publicPoolUfwCommand(ctx context.Context, port int) string {
	bridge := ""
	if resolved, err := publicPoolBridgeName(ctx); err == nil {
		bridge = resolved
	}
	return publicPoolUfwCommandForBridge(bridge, port)
}

func publicPoolUfwCommandForBridge(bridge string, port int) string {
	if port <= 0 {
		port = 8332
	}
	bridge = strings.TrimSpace(bridge)
	if bridge != "" {
		return fmt.Sprintf("sudo ufw allow in on %s to any port %d proto tcp", bridge, port)
	}
	return fmt.Sprintf("NET_ID=$(sudo docker network inspect publicpool_default --format '{{.Id}}') && BR=\"br-$(printf '%%.12s' \"$NET_ID\")\" && sudo ufw allow in on \"$BR\" to any port %d proto tcp", port)
}
