package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"lightningos-light/internal/system"
)

const (
	lnbitsImage = "lnbits/lnbits:latest"
	lnbitsPort  = 5000
)

type lnbitsPaths struct {
	Root        string
	DataDir     string
	ComposePath string
	EnvPath     string
}

type lnbitsApp struct {
	server *Server
}

func newLnbitsApp(s *Server) appHandler {
	return lnbitsApp{server: s}
}

func lnbitsDefinition() appDefinition {
	return appDefinition{
		ID:          "lnbits",
		Name:        "LNbits",
		Description: "Lightning wallet/accounts system and extension platform powered by your local LND.",
		Port:        lnbitsPort,
	}
}

func (a lnbitsApp) Definition() appDefinition {
	return lnbitsDefinition()
}

func (a lnbitsApp) Info(ctx context.Context) (appInfo, error) {
	def := a.Definition()
	info := newAppInfo(def)
	paths := lnbitsAppPaths()
	if !fileExists(paths.ComposePath) {
		return info, nil
	}
	info.Installed = true
	status, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, "lnbits")
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func (a lnbitsApp) Install(ctx context.Context) error {
	return a.server.installLnbits(ctx)
}

func (a lnbitsApp) Uninstall(ctx context.Context) error {
	return a.server.uninstallLnbits(ctx)
}

func (a lnbitsApp) Start(ctx context.Context) error {
	return a.server.startLnbits(ctx)
}

func (a lnbitsApp) Stop(ctx context.Context) error {
	return a.server.stopLnbits(ctx)
}

func lnbitsAppPaths() lnbitsPaths {
	root := filepath.Join(appsRoot, "lnbits")
	dataDir := filepath.Join(appsDataRoot, "lnbits", "data")
	return lnbitsPaths{
		Root:        root,
		DataDir:     dataDir,
		ComposePath: filepath.Join(root, "docker-compose.yaml"),
		EnvPath:     filepath.Join(root, ".env"),
	}
}

func (s *Server) installLnbits(ctx context.Context) error {
	if err := ensureDocker(ctx); err != nil {
		return err
	}
	paths := lnbitsAppPaths()
	if err := ensureLnbitsPaths(paths); err != nil {
		return err
	}
	if err := pullDockerImage(ctx, lnbitsImage); err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, lnbitsComposeContents(paths)); err != nil {
		return err
	}
	if err := ensureLnbitsEnv(paths); err != nil {
		return err
	}
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
		return err
	}
	if err := ensureLnbitsRestAccess(ctx); err != nil {
		return err
	}
	if err := ensureLnbitsUfwAccess(ctx); err != nil && s.logger != nil {
		s.logger.Printf("lnbits: ufw rule failed: %v", err)
	}
	return nil
}

func (s *Server) uninstallLnbits(ctx context.Context) error {
	paths := lnbitsAppPaths()
	if fileExists(paths.ComposePath) {
		_ = runCompose(ctx, paths.Root, paths.ComposePath, "down", "--remove-orphans")
	}
	if err := os.RemoveAll(paths.Root); err != nil {
		return fmt.Errorf("failed to remove app files: %w", err)
	}
	return nil
}

func (s *Server) startLnbits(ctx context.Context) error {
	paths := lnbitsAppPaths()
	if err := ensureLnbitsPaths(paths); err != nil {
		return err
	}
	if err := ensureLnbitsImage(ctx); err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, lnbitsComposeContents(paths)); err != nil {
		return err
	}
	if err := ensureLnbitsEnv(paths); err != nil {
		return err
	}
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
		return err
	}
	if err := ensureLnbitsRestAccess(ctx); err != nil {
		return err
	}
	if err := ensureLnbitsUfwAccess(ctx); err != nil && s.logger != nil {
		s.logger.Printf("lnbits: ufw rule failed: %v", err)
	}
	return nil
}

func (s *Server) stopLnbits(ctx context.Context) error {
	paths := lnbitsAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("LNbits is not installed")
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "stop")
}

func ensureLnbitsPaths(paths lnbitsPaths) error {
	if err := os.MkdirAll(paths.Root, 0750); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}
	if err := os.MkdirAll(paths.DataDir, 0750); err != nil {
		return fmt.Errorf("failed to create app data directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(paths.DataDir, "extensions"), 0750); err != nil {
		return fmt.Errorf("failed to create extension data directory: %w", err)
	}
	return nil
}

func ensureLnbitsImage(ctx context.Context) error {
	if strings.HasSuffix(lnbitsImage, ":latest") {
		return pullDockerImage(ctx, lnbitsImage)
	}
	return ensureDockerImage(ctx, lnbitsImage)
}

func lnbitsComposeContents(paths lnbitsPaths) string {
	return fmt.Sprintf(`services:
  lnbits:
    image: %s
    restart: unless-stopped
    env_file:
      - ./.env
    extra_hosts:
      - "host.docker.internal:host-gateway"
    ports:
      - "%d:%d"
    volumes:
      - %s:/app/data
      - /data/lnd:/data/lnd:ro
`, lnbitsImage, lnbitsPort, lnbitsPort, paths.DataDir)
}

func ensureLnbitsEnv(paths lnbitsPaths) error {
	defaults := [][2]string{
		{"LNBITS_BACKEND_WALLET_CLASS", "LndRestWallet"},
		{"LND_REST_ENDPOINT", "https://host.docker.internal:8080/"},
		{"LND_REST_CERT", "/data/lnd/tls.cert"},
		{"LND_REST_MACAROON", "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon"},
		{"LNBITS_EXTENSIONS_PATH", "/app/data/extensions"},
		{"LNBITS_HOST", "0.0.0.0"},
		{"LNBITS_PORT", "5000"},
		{"AUTH_HTTPS_ONLY", "false"},
	}
	if !fileExists(paths.EnvPath) {
		lines := make([]string, 0, len(defaults)+1)
		for _, kv := range defaults {
			lines = append(lines, kv[0]+"="+kv[1])
		}
		lines = append(lines, "")
		return writeFile(paths.EnvPath, strings.Join(lines, "\n"), 0600)
	}

	for _, kv := range defaults {
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
		if strings.TrimSpace(value) == "" {
			if err := setEnvValue(paths.EnvPath, kv[0], kv[1]); err != nil {
				return err
			}
		}
	}
	return nil
}

func envValueState(path string, key string) (bool, string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return false, "", fmt.Errorf("failed to read %s: %w", path, err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, key+"=") {
			return true, strings.TrimPrefix(line, key+"="), nil
		}
	}
	return false, "", nil
}

func ensureLnbitsRestAccess(ctx context.Context) error {
	gateways := []string{}
	bridgeIP, err := dockerGatewayIP(ctx)
	if err == nil && bridgeIP != "" {
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
	lines, changed := updateLndRestOptions(lines, gateways)
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

func updateLndRestOptions(lines []string, gateways []string) ([]string, bool) {
	uniqueGateways := []string{}
	for _, gateway := range gateways {
		gateway = strings.TrimSpace(gateway)
		if gateway == "" || stringInSlice(gateway, uniqueGateways) {
			continue
		}
		uniqueGateways = append(uniqueGateways, gateway)
	}

	cleaned := append([]string{}, lines...)
	insertIdx := -1
	for i, line := range lines {
		if !strings.EqualFold(strings.TrimSpace(line), "[Application Options]") {
			continue
		}
		insertIdx = i + 1
		managedEnd := insertIdx
		preserved := []string{}
		for managedEnd < len(lines) {
			trimmed := strings.TrimSpace(lines[managedEnd])
			if !isLndRestManagedLine(trimmed) {
				break
			}
			if !isLndRestAppManagedLine(trimmed) {
				preserved = append(preserved, lines[managedEnd])
			}
			managedEnd++
		}
		cleaned = append([]string{}, lines[:insertIdx]...)
		cleaned = append(cleaned, preserved...)
		cleaned = append(cleaned, lines[managedEnd:]...)
		break
	}
	if insertIdx == -1 {
		cleaned = append(cleaned, "[Application Options]")
		insertIdx = len(cleaned)
	}

	restSet := map[string]bool{}
	tlsExtraIPSet := map[string]bool{}
	tlsExtraDomainSet := map[string]bool{}
	for _, line := range cleaned {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "restlisten="):
			value := strings.TrimSpace(strings.TrimPrefix(trimmed, "restlisten="))
			if value != "" {
				restSet[value] = true
			}
		case strings.HasPrefix(trimmed, "tlsextraip="):
			value := strings.TrimSpace(strings.TrimPrefix(trimmed, "tlsextraip="))
			if value != "" {
				tlsExtraIPSet[value] = true
			}
		case strings.HasPrefix(trimmed, "tlsextradomain="):
			value := strings.TrimSpace(strings.TrimPrefix(trimmed, "tlsextradomain="))
			if value != "" {
				tlsExtraDomainSet[value] = true
			}
		}
	}

	block := []string{}
	for _, gateway := range uniqueGateways {
		if !tlsExtraIPSet[gateway] {
			block = append(block, "tlsextraip="+gateway)
		}
	}
	if !tlsExtraDomainSet["host.docker.internal"] {
		block = append(block, "tlsextradomain=host.docker.internal")
	}

	// LND already defaults to 127.0.0.1:8080 when no restlisten is configured,
	// so only add gateway listeners required for Docker access.
	hasWildcardRest8080 := restSet["0.0.0.0:8080"] || restSet["[::]:8080"] || restSet[":8080"] || restSet["*:8080"]
	if !hasWildcardRest8080 {
		for _, gateway := range uniqueGateways {
			value := gateway + ":8080"
			if !restSet[value] {
				block = append(block, "restlisten="+value)
			}
		}
	}

	updated := append([]string{}, cleaned[:insertIdx]...)
	updated = append(updated, block...)
	updated = append(updated, cleaned[insertIdx:]...)

	changed := len(updated) != len(lines)
	if !changed {
		for i := range updated {
			if updated[i] != lines[i] {
				changed = true
				break
			}
		}
	}
	return updated, changed
}

func isLndRestManagedLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "restlisten=") ||
		strings.HasPrefix(trimmed, "tlsextraip=") ||
		strings.HasPrefix(trimmed, "tlsextradomain=")
}

func isLndRestAppManagedLine(trimmed string) bool {
	switch {
	case strings.HasPrefix(trimmed, "tlsextradomain="):
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "tlsextradomain=")) == "host.docker.internal"
	case strings.HasPrefix(trimmed, "tlsextraip="):
		return isLikelyDockerGatewayIP(strings.TrimSpace(strings.TrimPrefix(trimmed, "tlsextraip=")))
	case strings.HasPrefix(trimmed, "restlisten="):
		return isLikelyDockerGatewayAddr(strings.TrimSpace(strings.TrimPrefix(trimmed, "restlisten=")))
	default:
		return false
	}
}

func isLikelyDockerGatewayAddr(value string) bool {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return false
	}
	if port != "8080" {
		return false
	}
	if host == "127.0.0.1" {
		return true
	}
	return isLikelyDockerGatewayIP(host)
}

func isLikelyDockerGatewayIP(value string) bool {
	ip := net.ParseIP(value)
	if ip == nil {
		return false
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}

	private := ip4[0] == 10 ||
		(ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31) ||
		(ip4[0] == 192 && ip4[1] == 168)
	if !private {
		return false
	}
	return ip4[3] == 1
}

func lnbitsNetworkGatewayIP(ctx context.Context) (string, error) {
	out, err := system.RunCommandWithSudo(ctx, "docker", "network", "inspect", "lnbits_default", "--format", "{{(index .IPAM.Config 0).Gateway}}")
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(out)
	if ip == "" || ip == "<no value>" {
		return "", errors.New("lnbits_default network gateway not found")
	}
	return ip, nil
}

func ensureLnbitsUfwAccess(ctx context.Context) error {
	statusOut, err := system.RunCommandWithSudo(ctx, "ufw", "status")
	if err != nil || !strings.Contains(strings.ToLower(statusOut), "status: active") {
		return nil
	}

	_, _ = system.RunCommandWithSudo(ctx, "ufw", "allow", fmt.Sprintf("%d/tcp", lnbitsPort))
	bridge, err := lnbitsBridgeName(ctx)
	if err != nil || bridge == "" {
		return nil
	}
	_, err = system.RunCommandWithSudo(ctx, "ufw", "allow", "in", "on", bridge, "to", "any", "port", "8080", "proto", "tcp")
	return err
}

func lnbitsBridgeName(ctx context.Context) (string, error) {
	out, err := system.RunCommandWithSudo(ctx, "docker", "network", "inspect", "lnbits_default", "--format", "{{.Id}}")
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(out)
	if id == "" || id == "<no value>" {
		return "", errors.New("lnbits_default network id not found")
	}
	if len(id) > 12 {
		id = id[:12]
	}
	return "br-" + id, nil
}
