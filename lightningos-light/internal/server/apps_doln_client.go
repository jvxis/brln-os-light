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
	dolnClientImage   = "pagcoin/doln-client:latest"
	dolnClientPort    = 1080
	dolnClientWebPort = 1081
)

type dolnClientPaths struct {
	Root        string
	DataDir     string
	ComposePath string
	EnvPath     string
}

type dolnClientApp struct {
	server *Server
}

func newDolnClientApp(s *Server) appHandler {
	return dolnClientApp{server: s}
}

func dolnClientDefinition() appDefinition {
	return appDefinition{
		ID:          "dolnclient",
		Name:        "DDLNS Client",
		Description: "Consume DNS over Lightning Network - alfa v0.1 beta. Provides a SOCKS5 proxy that resolves DNS via keysend to a DDLNS server.",
		Port:        dolnClientWebPort,
	}
}

func (a dolnClientApp) Definition() appDefinition {
	return dolnClientDefinition()
}

func (a dolnClientApp) Info(ctx context.Context) (appInfo, error) {
	def := a.Definition()
	info := newAppInfo(def)
	paths := dolnClientAppPaths()
	if !fileExists(paths.ComposePath) {
		return info, nil
	}
	info.Installed = true
	status, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, "doln-client")
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func (a dolnClientApp) Install(ctx context.Context) error {
	return a.server.installDolnClient(ctx)
}

func (a dolnClientApp) Uninstall(ctx context.Context) error {
	return a.server.uninstallDolnClient(ctx)
}

func (a dolnClientApp) Start(ctx context.Context) error {
	return a.server.startDolnClient(ctx)
}

func (a dolnClientApp) Stop(ctx context.Context) error {
	return a.server.stopDolnClient(ctx)
}

func dolnClientAppPaths() dolnClientPaths {
	root := filepath.Join(appsRoot, "dolnclient")
	dataDir := filepath.Join(appsDataRoot, "dolnclient")
	return dolnClientPaths{
		Root:        root,
		DataDir:     dataDir,
		ComposePath: filepath.Join(root, "docker-compose.yaml"),
		EnvPath:     filepath.Join(root, ".env"),
	}
}

func (s *Server) installDolnClient(ctx context.Context) error {
	if err := ensureDocker(ctx); err != nil {
		return err
	}
	paths := dolnClientAppPaths()
	if err := os.MkdirAll(paths.Root, 0750); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}
	if err := os.MkdirAll(paths.DataDir, 0750); err != nil {
		return fmt.Errorf("failed to create app data directory: %w", err)
	}
	if err := ensureDockerImage(ctx, dolnClientImage); err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, dolnClientComposeContents(paths)); err != nil {
		return err
	}
	if err := ensureDolnClientEnv(paths); err != nil {
		return err
	}
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
		return err
	}
	if err := ensureDolnClientGrpcAccess(ctx); err != nil {
		return err
	}
	if err := ensureDolnClientUfwAccess(ctx); err != nil && s.logger != nil {
		s.logger.Printf("dolnclient: ufw rule failed: %v", err)
	}
	if err := updateDolnClientGrpcHost(ctx, paths); err != nil && s.logger != nil {
		s.logger.Printf("dolnclient: grpc host update failed: %v", err)
	}
	return nil
}

func (s *Server) uninstallDolnClient(ctx context.Context) error {
	paths := dolnClientAppPaths()
	if fileExists(paths.ComposePath) {
		_ = runCompose(ctx, paths.Root, paths.ComposePath, "down", "--remove-orphans")
	}
	if err := os.RemoveAll(paths.Root); err != nil {
		return fmt.Errorf("failed to remove app files: %w", err)
	}
	return nil
}

func (s *Server) startDolnClient(ctx context.Context) error {
	paths := dolnClientAppPaths()
	if err := os.MkdirAll(paths.Root, 0750); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}
	if err := os.MkdirAll(paths.DataDir, 0750); err != nil {
		return fmt.Errorf("failed to create app data directory: %w", err)
	}
	if err := ensureDockerImage(ctx, dolnClientImage); err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, dolnClientComposeContents(paths)); err != nil {
		return err
	}
	if err := ensureDolnClientEnv(paths); err != nil {
		return err
	}
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
		return err
	}
	if err := ensureDolnClientGrpcAccess(ctx); err != nil {
		return err
	}
	if err := ensureDolnClientUfwAccess(ctx); err != nil && s.logger != nil {
		s.logger.Printf("dolnclient: ufw rule failed: %v", err)
	}
	if err := updateDolnClientGrpcHost(ctx, paths); err != nil && s.logger != nil {
		s.logger.Printf("dolnclient: grpc host update failed: %v", err)
	}
	return nil
}

func (s *Server) stopDolnClient(ctx context.Context) error {
	paths := dolnClientAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("DDLNS Client is not installed")
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "stop")
}

func dolnClientComposeContents(paths dolnClientPaths) string {
	return fmt.Sprintf(`services:
  doln-client:
    image: %s
    restart: unless-stopped
    env_file:
      - ./.env
    extra_hosts:
      - "host.docker.internal:host-gateway"
    ports:
      - "%d:%d"
      - "%d:%d"
    volumes:
      - /data/lnd:/data/lnd:ro
      - %s:/data/doln
`, dolnClientImage, dolnClientPort, dolnClientPort, dolnClientWebPort, dolnClientWebPort, paths.DataDir)
}

func ensureDolnClientEnv(paths dolnClientPaths) error {
	defaults := [][2]string{
		{"LND_GRPC_HOST", "host.docker.internal:10009"},
		{"LND_CERT_PATH", "/data/lnd/tls.cert"},
		{"LND_MACAROON_PATH", "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon"},
		{"DOLN_SERVER_PUBKEYS", "03aae60ebc0d009ef1bd3bcd5c66611d66cd235bff9ee7d2ce7ffec89fb369e981"},
		{"SOCKS5_LISTEN", "0.0.0.0:1080"},
		{"DNS_TIMEOUT_SECONDS", "10"},
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

func ensureDolnClientGrpcAccess(ctx context.Context) error {
	gateways := []string{}
	bridgeIP, err := dockerGatewayIP(ctx)
	if err == nil && bridgeIP != "" {
		gateways = append(gateways, bridgeIP)
	}
	dolnGatewayIP, err := dolnClientNetworkGatewayIP(ctx)
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

func dolnClientNetworkGatewayIP(ctx context.Context) (string, error) {
	out, err := system.RunCommandWithSudo(ctx, "docker", "network", "inspect", "dolnclient_default", "--format", "{{(index .IPAM.Config 0).Gateway}}")
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(out)
	if ip == "" || ip == "<no value>" {
		return "", errors.New("dolnclient_default network gateway not found")
	}
	return ip, nil
}

func ensureDolnClientUfwAccess(ctx context.Context) error {
	statusOut, err := system.RunCommandWithSudo(ctx, "ufw", "status")
	if err != nil || !strings.Contains(strings.ToLower(statusOut), "status: active") {
		return nil
	}
	_, _ = system.RunCommandWithSudo(ctx, "ufw", "allow", fmt.Sprintf("%d/tcp", dolnClientPort))
	_, _ = system.RunCommandWithSudo(ctx, "ufw", "allow", fmt.Sprintf("%d/tcp", dolnClientWebPort))
	bridge, err := dolnClientBridgeName(ctx)
	if err != nil || bridge == "" {
		return nil
	}
	_, err = system.RunCommandWithSudo(ctx, "ufw", "allow", "in", "on", bridge, "to", "any", "port", "10009", "proto", "tcp")
	return err
}

func dolnClientBridgeName(ctx context.Context) (string, error) {
	out, err := system.RunCommandWithSudo(ctx, "docker", "network", "inspect", "dolnclient_default", "--format", "{{.Id}}")
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(out)
	if id == "" || id == "<no value>" {
		return "", errors.New("dolnclient_default network id not found")
	}
	if len(id) > 12 {
		id = id[:12]
	}
	return "br-" + id, nil
}

func updateDolnClientGrpcHost(ctx context.Context, paths dolnClientPaths) error {
	gatewayIP, err := dolnClientNetworkGatewayIP(ctx)
	if err != nil || gatewayIP == "" {
		return err
	}
	grpcHost := gatewayIP + ":10009"
	if err := setEnvValue(paths.EnvPath, "LND_GRPC_HOST", grpcHost); err != nil {
		return err
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d")
}
