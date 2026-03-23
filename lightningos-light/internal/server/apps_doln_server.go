package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lightningos-light/internal/system"
)

const (
	dolnServerAdguardImage = "adguard/adguardhome:latest"
	dolnServerDaemonImage  = "pagcoin/doln-server:latest"
	dolnServerPort         = 3000
)

type dolnServerPaths struct {
	Root        string
	DataDir     string
	WorkDir     string
	ConfDir     string
	ComposePath string
	EnvPath     string
}

type dolnServerApp struct {
	server *Server
}

func newDolnServerApp(s *Server) appHandler {
	return dolnServerApp{server: s}
}

func dolnServerDefinition() appDefinition {
	return appDefinition{
		ID:          "dolnserver",
		Name:        "DDLNS Server",
		Description: "Sell DNS over Lightning Network - alfa v0.1 beta. Resolves DNS queries received via keysend using AdGuard Home.",
		Port:        dolnServerPort,
	}
}

func (a dolnServerApp) Definition() appDefinition {
	return dolnServerDefinition()
}

func (a dolnServerApp) Info(ctx context.Context) (appInfo, error) {
	def := a.Definition()
	info := newAppInfo(def)
	paths := dolnServerAppPaths()
	if !fileExists(paths.ComposePath) {
		return info, nil
	}
	info.Installed = true
	status, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, "doln-server")
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func (a dolnServerApp) Install(ctx context.Context) error {
	return a.server.installDolnServer(ctx)
}

func (a dolnServerApp) Uninstall(ctx context.Context) error {
	return a.server.uninstallDolnServer(ctx)
}

func (a dolnServerApp) Start(ctx context.Context) error {
	return a.server.startDolnServer(ctx)
}

func (a dolnServerApp) Stop(ctx context.Context) error {
	return a.server.stopDolnServer(ctx)
}

func dolnServerAppPaths() dolnServerPaths {
	root := filepath.Join(appsRoot, "dolnserver")
	dataDir := filepath.Join(appsDataRoot, "dolnserver")
	return dolnServerPaths{
		Root:        root,
		DataDir:     dataDir,
		WorkDir:     filepath.Join(dataDir, "work"),
		ConfDir:     filepath.Join(dataDir, "conf"),
		ComposePath: filepath.Join(root, "docker-compose.yaml"),
		EnvPath:     filepath.Join(root, ".env"),
	}
}

func (s *Server) installDolnServer(ctx context.Context) error {
	if err := ensureDocker(ctx); err != nil {
		return err
	}
	paths := dolnServerAppPaths()
	if err := ensureDolnServerPaths(paths); err != nil {
		return err
	}
	if err := ensureDolnServerImages(ctx); err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, dolnServerComposeContents(paths)); err != nil {
		return err
	}
	if err := ensureDolnServerEnv(paths); err != nil {
		return err
	}
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
		return err
	}
	if err := ensureDolnServerGrpcAccess(ctx); err != nil {
		return err
	}
	if err := ensureDolnServerUfwAccess(ctx); err != nil && s.logger != nil {
		s.logger.Printf("dolnserver: ufw rule failed: %v", err)
	}
	if err := updateDolnServerGrpcHost(ctx, paths); err != nil && s.logger != nil {
		s.logger.Printf("dolnserver: grpc host update failed: %v", err)
	}
	return nil
}

func (s *Server) uninstallDolnServer(ctx context.Context) error {
	paths := dolnServerAppPaths()
	if fileExists(paths.ComposePath) {
		_ = runCompose(ctx, paths.Root, paths.ComposePath, "down", "--remove-orphans")
	}
	if err := os.RemoveAll(paths.Root); err != nil {
		return fmt.Errorf("failed to remove app files: %w", err)
	}
	return nil
}

func (s *Server) startDolnServer(ctx context.Context) error {
	paths := dolnServerAppPaths()
	if err := ensureDolnServerPaths(paths); err != nil {
		return err
	}
	if err := ensureDolnServerImages(ctx); err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, dolnServerComposeContents(paths)); err != nil {
		return err
	}
	if err := ensureDolnServerEnv(paths); err != nil {
		return err
	}
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
		return err
	}
	if err := ensureDolnServerGrpcAccess(ctx); err != nil {
		return err
	}
	if err := ensureDolnServerUfwAccess(ctx); err != nil && s.logger != nil {
		s.logger.Printf("dolnserver: ufw rule failed: %v", err)
	}
	if err := updateDolnServerGrpcHost(ctx, paths); err != nil && s.logger != nil {
		s.logger.Printf("dolnserver: grpc host update failed: %v", err)
	}
	return nil
}

func (s *Server) stopDolnServer(ctx context.Context) error {
	paths := dolnServerAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("DDLNS Server is not installed")
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "stop")
}

func ensureDolnServerPaths(paths dolnServerPaths) error {
	for _, dir := range []string{paths.Root, paths.DataDir, paths.WorkDir, paths.ConfDir} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}
	return nil
}

func ensureDolnServerImages(ctx context.Context) error {
	if err := ensureDockerImage(ctx, dolnServerAdguardImage); err != nil {
		return err
	}
	return ensureDockerImage(ctx, dolnServerDaemonImage)
}

func dolnServerComposeContents(paths dolnServerPaths) string {
	return fmt.Sprintf(`services:
  adguard:
    image: %s
    restart: unless-stopped
    volumes:
      - %s:/opt/adguardhome/work
      - %s:/opt/adguardhome/conf
  doln-server:
    image: %s
    restart: unless-stopped
    depends_on:
      - adguard
    env_file:
      - ./.env
    extra_hosts:
      - "host.docker.internal:host-gateway"
    ports:
      - "%d:%d"
    volumes:
      - /data/lnd:/data/lnd:ro
      - %s:/data/doln
`, dolnServerAdguardImage, paths.WorkDir, paths.ConfDir,
		dolnServerDaemonImage, dolnServerPort, dolnServerPort, paths.DataDir)
}

func ensureDolnServerEnv(paths dolnServerPaths) error {
	defaults := [][2]string{
		{"LND_GRPC_HOST", "host.docker.internal:10009"},
		{"LND_CERT_PATH", "/data/lnd/tls.cert"},
		{"LND_MACAROON_PATH", "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon"},
		{"ADGUARD_DNS_HOST", "adguard"},
		{"ADGUARD_DNS_PORT", "53"},
		{"KEYSEND_AMOUNT_SAT", "1"},
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

func ensureDolnServerGrpcAccess(ctx context.Context) error {
	gateways := []string{}
	bridgeIP, err := dockerGatewayIP(ctx)
	if err == nil && bridgeIP != "" {
		gateways = append(gateways, bridgeIP)
	}
	dolnGatewayIP, err := dolnServerNetworkGatewayIP(ctx)
	if err == nil && dolnGatewayIP != "" && !stringInSlice(dolnGatewayIP, gateways) {
		gateways = append(gateways, dolnGatewayIP)
	}
	if len(gateways) == 0 {
		return errors.New("unable to determine docker gateway IPs")
	}
	content, err := os.ReadFile(lndConfPath)
	if err != nil {
		return fmt.Errorf("failed to read lnd.conf: %w", err)
	}
	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	lines, changed := updateLndGrpcOptions(lines, gateways)
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

func dolnServerNetworkGatewayIP(ctx context.Context) (string, error) {
	out, err := system.RunCommandWithSudo(ctx, "docker", "network", "inspect", "dolnserver_default", "--format", "{{(index .IPAM.Config 0).Gateway}}")
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(out)
	if ip == "" || ip == "<no value>" {
		return "", errors.New("dolnserver_default network gateway not found")
	}
	return ip, nil
}

func ensureDolnServerUfwAccess(ctx context.Context) error {
	statusOut, err := system.RunCommandWithSudo(ctx, "ufw", "status")
	if err != nil || !strings.Contains(strings.ToLower(statusOut), "status: active") {
		return nil
	}
	_, _ = system.RunCommandWithSudo(ctx, "ufw", "allow", fmt.Sprintf("%d/tcp", dolnServerPort))
	bridge, err := dolnServerBridgeName(ctx)
	if err != nil || bridge == "" {
		return nil
	}
	_, err = system.RunCommandWithSudo(ctx, "ufw", "allow", "in", "on", bridge, "to", "any", "port", "10009", "proto", "tcp")
	return err
}

func dolnServerBridgeName(ctx context.Context) (string, error) {
	out, err := system.RunCommandWithSudo(ctx, "docker", "network", "inspect", "dolnserver_default", "--format", "{{.Id}}")
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(out)
	if id == "" || id == "<no value>" {
		return "", errors.New("dolnserver_default network id not found")
	}
	if len(id) > 12 {
		id = id[:12]
	}
	return "br-" + id, nil
}

func updateDolnServerGrpcHost(ctx context.Context, paths dolnServerPaths) error {
	gatewayIP, err := dolnServerNetworkGatewayIP(ctx)
	if err != nil || gatewayIP == "" {
		return err
	}
	grpcHost := gatewayIP + ":10009"
	if err := setEnvValue(paths.EnvPath, "LND_GRPC_HOST", grpcHost); err != nil {
		return err
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d")
}
